package core

import (
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Fetcher downloads installers into the cache folder. It tries every URL of a
// release in order, resumes partial downloads and checks size and checksum
// before a file is accepted.
type Fetcher struct {
	CacheDir    string
	GithubProxy string
	Sim         bool
	Client      *http.Client
}

type Progress func(done, total int64, source string)

func NewFetcher(cacheDir, githubProxy string, sim bool) *Fetcher {
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		MaxIdleConns:          8,
		IdleConnTimeout:       60 * time.Second,
	}
	return &Fetcher{CacheDir: cacheDir, GithubProxy: strings.TrimSpace(githubProxy), Sim: sim, Client: &http.Client{Transport: tr}}
}

// UserAgent is sent with every download request. It must not look like a
// browser: download.jetbrains.com answers 404 to browser user agents asking
// for an installer directly.
const UserAgent = "DevNest/" + AppVersion

const userAgent = UserAgent

// candidates expands the URL list: with a GitHub accelerator configured, the
// accelerated address is tried before the direct one.
//
// The catalogue lists mirrors inside China first and the publisher's own
// address last. Outside China that is the slow order, so setting the
// environment variable DEVNEST_SOURCES=official turns it around.
func (f *Fetcher) candidates(urls []string) []string {
	if strings.EqualFold(os.Getenv("DEVNEST_SOURCES"), "official") {
		urls = slices.Clone(urls)
		slices.Reverse(urls)
	}
	var out []string
	for _, u := range urls {
		if f.GithubProxy != "" && (strings.HasPrefix(u, "https://github.com/") || strings.HasPrefix(u, "https://raw.githubusercontent.com/")) {
			out = append(out, strings.TrimRight(f.GithubProxy, "/")+"/"+u)
		}
		out = append(out, u)
	}
	return out
}

func hostOf(u string) string {
	if p, err := url.Parse(u); err == nil {
		return p.Host
	}
	return u
}

func hashFile(path string, h hash.Hash) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

var hexWord = regexp.MustCompile(`(?i)\b([0-9a-f]{128}|[0-9a-f]{64})\b`)

// GetText fetches a small text resource (checksum files, version indexes).
func (f *Fetcher) GetText(ctx context.Context, u string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := f.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return string(b), err
}

// expected works out which checksum the file must have. It returns the
// algorithm ("sha256"/"sha512") and the hex digest, or "" when upstream
// publishes none.
func (f *Fetcher) expected(ctx context.Context, r Release, logf func(string, ...any)) (string, string) {
	switch {
	case r.SHA256 != "":
		return "sha256", strings.ToLower(r.SHA256)
	case r.SHA512 != "":
		return "sha512", strings.ToLower(r.SHA512)
	case r.SumURL != "" && !f.Sim:
		txt, err := f.GetText(ctx, r.SumURL)
		if err != nil {
			logf("取在线校验值失败（%v），只校验文件大小", err)
			return "", ""
		}
		if m := hexWord.FindString(txt); m != "" {
			if len(m) == 128 {
				return "sha512", strings.ToLower(m)
			}
			return "sha256", strings.ToLower(m)
		}
		if m := spacedDigest(txt); len(m) == 128 {
			return "sha512", m
		} else if m != "" {
			return "sha256", m
		}
		logf("在线校验文件里没有找到校验值，只校验文件大小")
	}
	return "", ""
}

func verify(path string, r Release, algo, sum string) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if r.Size > 0 && fi.Size() != r.Size {
		return fmt.Errorf("文件大小不对：应为 %d 字节，实际 %d 字节", r.Size, fi.Size())
	}
	if sum == "" {
		return nil
	}
	var h hash.Hash = sha256.New()
	if algo == "sha512" {
		h = sha512.New()
	}
	got, err := hashFile(path, h)
	if err != nil {
		return err
	}
	if got != sum {
		return fmt.Errorf("%s 校验值不对：应为 %s，实际 %s", algo, sum, got)
	}
	return nil
}

// Cached reports whether a verified copy of the release is in the cache.
func (f *Fetcher) Cached(r Release) bool {
	path := filepath.Join(f.CacheDir, r.File)
	fi, err := os.Stat(path)
	if err != nil {
		return false
	}
	mark, err := os.ReadFile(path + ".ok")
	return err == nil && strings.TrimSpace(string(mark)) == fmt.Sprint(fi.Size())
}

// Get returns the path of a verified copy of the release, downloading it when
// it is not cached yet.
func (f *Fetcher) Get(ctx context.Context, r Release, progress Progress, logf func(string, ...any)) (string, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if progress == nil {
		progress = func(int64, int64, string) {}
	}
	if r.File == "" {
		return "", errors.New("这个版本没有配置安装包文件名")
	}
	if err := os.MkdirAll(f.CacheDir, 0o755); err != nil {
		return "", err
	}
	dest := filepath.Join(f.CacheDir, r.File)
	if f.Cached(r) {
		logf("使用已缓存的安装包 %s", r.File)
		progress(r.Size, r.Size, "缓存")
		return dest, nil
	}
	if f.Sim {
		return dest, f.simGet(ctx, r, dest, progress, logf)
	}
	algo, sum := f.expected(ctx, r, logf)

	// A file dropped into the cache by hand (or imported) is accepted once it
	// passes the same checks as a download.
	if _, err := os.Stat(dest); err == nil {
		if err := verify(dest, r, algo, sum); err == nil {
			logf("缓存里的 %s 校验通过", r.File)
			f.markOK(dest)
			return dest, nil
		} else {
			logf("缓存里的 %s 不可用（%v），重新下载", r.File, err)
			os.Remove(dest)
		}
	}

	part := dest + ".part"
	var lastErr error
	for _, u := range f.candidates(r.URLs) {
		for attempt := 1; attempt <= 2; attempt++ {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			logf("下载 %s（来源 %s）", r.File, hostOf(u))
			err := f.download(ctx, u, part, r.Size, progress)
			if err == nil {
				if err = verify(part, r, algo, sum); err != nil {
					os.Remove(part)
				}
			}
			if err == nil {
				if err := os.Rename(part, dest); err != nil {
					return "", err
				}
				f.markOK(dest)
				if sum == "" {
					logf("下载完成。上游没有提供校验值，只核对了文件大小")
				} else {
					logf("下载完成，%s 校验通过", algo)
				}
				return dest, nil
			}
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			lastErr = err
			logf("从 %s 下载失败：%v", hostOf(u), err)
			var he *httpError
			if errors.As(err, &he) && he.code >= 400 && he.code < 500 {
				break // the file is not there; another try will not help
			}
		}
	}
	if lastErr == nil {
		lastErr = errors.New("没有可用的下载地址")
	}
	return "", fmt.Errorf("下载 %s 失败：%w。可以在「设置」里导入手动下载好的安装包后重试", r.File, lastErr)
}

func (f *Fetcher) markOK(path string) {
	if fi, err := os.Stat(path); err == nil {
		os.WriteFile(path+".ok", []byte(fmt.Sprint(fi.Size())), 0o644)
	}
}

type httpError struct{ code int }

func (e *httpError) Error() string { return fmt.Sprintf("服务器返回 HTTP %d", e.code) }

// stallReader fails a download that stops delivering data.
type stallReader struct {
	r      io.Reader
	timer  *time.Timer
	limit  time.Duration
	cancel context.CancelFunc
}

func (s *stallReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if n > 0 {
		s.timer.Reset(s.limit)
	}
	return n, err
}

func (f *Fetcher) download(parent context.Context, u, part string, want int64, progress Progress) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	var have int64
	if fi, err := os.Stat(part); err == nil {
		have = fi.Size()
		if want > 0 && have >= want {
			if have == want {
				return nil
			}
			os.Remove(part)
			have = 0
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case http.StatusPartialContent:
		flags |= os.O_APPEND
	case http.StatusOK:
		have = 0
		flags |= os.O_TRUNC
	case http.StatusRequestedRangeNotSatisfiable:
		os.Remove(part)
		return errors.New("断点续传位置无效，已丢弃未完成的文件")
	default:
		return &httpError{resp.StatusCode}
	}
	if ct := resp.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/html") {
		return errors.New("服务器返回的是网页而不是安装包")
	}
	total := want
	if total <= 0 && resp.ContentLength > 0 {
		total = have + resp.ContentLength
	}
	out, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	defer out.Close()

	const stall = 60 * time.Second
	timer := time.AfterFunc(stall, cancel)
	defer timer.Stop()
	body := &stallReader{r: resp.Body, timer: timer, limit: stall, cancel: cancel}
	buf := make([]byte, 256<<10)
	done := have
	last := time.Now()
	progress(done, total, hostOf(u))
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			if _, werr := out.Write(buf[:n]); werr != nil {
				return fmt.Errorf("写入磁盘失败：%w", werr)
			}
			done += int64(n)
			if time.Since(last) > 200*time.Millisecond {
				progress(done, total, hostOf(u))
				last = time.Now()
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			if parent.Err() != nil {
				return parent.Err()
			}
			if ctx.Err() != nil {
				return errors.New("连接 60 秒没有数据，已中断")
			}
			return rerr
		}
	}
	progress(done, total, hostOf(u))
	return out.Sync()
}

func (f *Fetcher) simGet(ctx context.Context, r Release, dest string, progress Progress, logf func(string, ...any)) error {
	src := "模拟"
	if len(r.URLs) > 0 {
		src = hostOf(r.URLs[0])
	}
	logf("[模拟] 下载 %s（来源 %s）", r.File, src)
	total := r.Size
	if total <= 0 {
		total = 50 << 20
	}
	for i := 1; i <= 8; i++ {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(90 * time.Millisecond):
		}
		progress(total*int64(i)/8, total, src)
	}
	if err := os.WriteFile(dest, []byte("simulated installer "+r.File+"\n"), 0o644); err != nil {
		return err
	}
	f.markOK(dest)
	return nil
}

// Import copies an installer the user downloaded by hand into the cache after
// checking it against the release.
func (f *Fetcher) Import(ctx context.Context, r Release, src string) error {
	algo, sum := f.expected(ctx, r, func(string, ...any) {})
	if !f.Sim {
		if err := verify(src, r, algo, sum); err != nil {
			return fmt.Errorf("这个文件不是 %s：%w", r.File, err)
		}
	}
	if err := os.MkdirAll(f.CacheDir, 0o755); err != nil {
		return err
	}
	dest := filepath.Join(f.CacheDir, r.File)
	if err := copyFile(src, dest); err != nil {
		return err
	}
	f.markOK(dest)
	return nil
}

func copyFile(src, dest string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
