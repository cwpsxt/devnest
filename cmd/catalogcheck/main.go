// Command catalogcheck asks every download address in the catalogue whether
// the file is still there and still has the size the catalogue records. It
// downloads nothing: one byte per address is requested.
//
//	go run ./cmd/catalogcheck            # all packages
//	go run ./cmd/catalogcheck mysql jdk  # only these
//	go run ./cmd/catalogcheck -strict    # a dead mirror fails the run too
//	go run ./cmd/catalogcheck -list      # only print the addresses
//
// The exit status is 1 when a release cannot be downloaded from any of its
// addresses, when an address serves a file of a different size, or when a
// checksum file is missing.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"devnest/internal/core"
	_ "devnest/internal/recipes"
)

// userAgent is what the downloader sends; -ua overrides it to try another.
var userAgent = core.UserAgent

// hostLocks lets only one request at a time go to a host. Several mirrors
// answer 403 when one address opens parallel connections.
var (
	hostMu    sync.Mutex
	hostLocks = map[string]*sync.Mutex{}
)

func lockHost(u string) func() {
	hostMu.Lock()
	l := hostLocks[host(u)]
	if l == nil {
		l = &sync.Mutex{}
		hostLocks[host(u)] = l
	}
	hostMu.Unlock()
	l.Lock()
	return l.Unlock
}

var client = &http.Client{
	Timeout: 45 * time.Second,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
	},
}

type probe struct {
	url  string
	size int64 // -1 when the server did not say
	err  string
}

type row struct {
	pkg, version string
	want         int64
	probes       []probe
	sum          string // "", "ok" or a problem
	pinned       bool
}

var totalRx = regexp.MustCompile(`/(\d+)\s*$`)

// sizeOf asks for the first byte only and reads the full size from the
// Content-Range header. Servers that ignore Range answer 200 with the full
// length, which works just as well; the body is never read.
func sizeOf(ctx context.Context, u string) (int64, error) {
	defer lockHost(u)()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return -1, err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Range", "bytes=0-0")
	resp, err := client.Do(req)
	if err != nil {
		return -1, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusPartialContent:
		if m := totalRx.FindStringSubmatch(resp.Header.Get("Content-Range")); m != nil {
			n, _ := strconv.ParseInt(m[1], 10, 64)
			return n, nil
		}
		return -1, nil
	case http.StatusOK:
		if ct := resp.Header.Get("Content-Type"); strings.HasPrefix(ct, "text/html") {
			return -1, fmt.Errorf("got a web page (%s) instead of a file", ct)
		}
		return resp.ContentLength, nil
	}
	return -1, fmt.Errorf("HTTP %d", resp.StatusCode)
}

var hexRx = regexp.MustCompile(`(?i)\b[0-9a-f]{64}\b|\b[0-9a-f]{128}\b`)

func checkSum(ctx context.Context, r core.Release) string {
	defer lockHost(r.SumURL)()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.SumURL, nil)
	if err != nil {
		return err.Error()
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return short(err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Sprintf("HTTP %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	txt := string(body)
	if !hexRx.MatchString(txt) && !hexRx.MatchString(strings.Join(strings.Fields(txt), "")) {
		return "no digest in the file"
	}
	return "ok"
}

func short(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if i := strings.LastIndex(s, ": "); i >= 0 && len(s) > 70 {
		s = s[i+2:]
	}
	if len(s) > 70 {
		s = s[:70] + "…"
	}
	return s
}

func host(u string) string {
	if p, err := url.Parse(u); err == nil {
		return p.Host
	}
	return u
}

func mb(n int64) string {
	if n < 0 {
		return "?"
	}
	return fmt.Sprintf("%.1f MB", float64(n)/1048576)
}

func main() {
	strict := flag.Bool("strict", false, "fail when any single address is unreachable, even if another one works")
	workers := flag.Int("j", 8, "how many addresses to ask at the same time")
	flag.StringVar(&userAgent, "ua", userAgent, "User-Agent to send")
	list := flag.Bool("list", false, "print every address and exit without asking any server")
	flag.Parse()
	only := map[string]bool{}
	for _, a := range flag.Args() {
		only[a] = true
	}
	for id := range only {
		if core.Lookup(id) == nil {
			fmt.Fprintf(os.Stderr, "unknown package %q\n", id)
			os.Exit(2)
		}
	}

	var rows []*row
	for _, p := range core.Packages() {
		if len(only) > 0 && !only[p.ID] {
			continue
		}
		for _, r := range p.Releases {
			rw := &row{pkg: p.ID, version: r.Version, want: r.Size, pinned: r.SHA256 != "" || r.SHA512 != "" || r.SumURL != ""}
			for _, u := range r.URLs {
				rw.probes = append(rw.probes, probe{url: u, size: -1})
			}
			rows = append(rows, rw)
		}
	}

	if *list {
		for _, rw := range rows {
			for _, p := range rw.probes {
				fmt.Printf("%s\t%s\t%s\n", rw.pkg, rw.version, p.url)
			}
		}
		return
	}

	ctx := context.Background()
	sem := make(chan struct{}, *workers)
	var wg sync.WaitGroup
	for _, rw := range rows {
		for i := range rw.probes {
			wg.Add(1)
			go func(p *probe) {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				n, err := sizeOf(ctx, p.url)
				if err != nil {
					// One more try: mirrors drop connections now and then.
					time.Sleep(2 * time.Second)
					n, err = sizeOf(ctx, p.url)
				}
				p.size = n
				if err != nil {
					p.err = short(err.Error())
				}
			}(&rw.probes[i])
		}
	}
	for _, p := range core.Packages() {
		for _, r := range p.Releases {
			if r.SumURL == "" || (len(only) > 0 && !only[p.ID]) {
				continue
			}
			for _, rw := range rows {
				if rw.pkg == p.ID && rw.version == r.Version {
					wg.Add(1)
					go func(rw *row, r core.Release) {
						defer wg.Done()
						sem <- struct{}{}
						defer func() { <-sem }()
						rw.sum = checkSum(ctx, r)
					}(rw, r)
				}
			}
		}
	}
	wg.Wait()

	bad, warn := 0, 0
	for _, rw := range rows {
		alive := 0
		var lines []string
		failed := false
		for _, p := range rw.probes {
			switch {
			case p.err != "":
				lines = append(lines, fmt.Sprintf("      DEAD  %-32s %s", host(p.url), p.err))
				if *strict {
					failed = true
				} else {
					warn++
				}
			case rw.want > 0 && p.size >= 0 && p.size != rw.want:
				lines = append(lines, fmt.Sprintf("      SIZE  %-32s serves %d bytes, catalogue says %d", host(p.url), p.size, rw.want))
				failed = true
			default:
				alive++
				note := ""
				if p.size < 0 {
					note = "size not reported"
				} else if rw.want == 0 {
					note = fmt.Sprintf("%d bytes (catalogue records no size)", p.size)
				}
				lines = append(lines, fmt.Sprintf("      ok    %-32s %s", host(p.url), note))
			}
		}
		if alive == 0 {
			failed = true
		}
		if rw.sum != "" && rw.sum != "ok" {
			lines = append(lines, "      SUM   checksum file: "+rw.sum)
			failed = true
		}
		status := "ok  "
		if failed {
			status = "FAIL"
			bad++
		}
		integrity := "size only"
		if rw.pinned {
			integrity = "checksum"
		}
		fmt.Printf("%s  %-14s %-12s %10s  %d/%d addresses  %s\n", status, rw.pkg, rw.version, mb(rw.want), alive, len(rw.probes), integrity)
		for _, l := range lines {
			fmt.Println(l)
		}
	}
	fmt.Printf("\n%d releases checked, %d failed, %d dead mirror addresses tolerated\n", len(rows), bad, warn)
	if bad > 0 {
		os.Exit(1)
	}
}
