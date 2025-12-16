package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
)

// 15 adet örnek URL (ödevdeki "15 farklı site üstünde dene" kısmı için)
// İstersen bunları değiştirebilirsin.
var defaultTargets = []string{
	"https://example.com/",
	"https://httpbin.org/html",
	"https://www.iana.org/domains/reserved",
	"https://www.rfc-editor.org/",
	"https://www.ietf.org/",
	"https://go.dev/",
	"https://pkg.go.dev/",
	"https://docs.python.org/tr/3/",
	"https://git-scm.com/book/tr/v2",
	"https://developer.mozilla.org/tr/",
	"https://learn.microsoft.com/tr-tr/",
	"https://tr.wikipedia.org/wiki/Anasayfa",
	"https://tr.wikipedia.org/wiki/Türkiye",
	"https://tr.wiktionary.org/wiki/Vikis%C3%B6zl%C3%BCk:Anasayfa",
	"https://tr.wikiquote.org/wiki/Anasayfa",
}

type Result struct {
	URL            string `json:"url"`
	OutDir         string `json:"out_dir"`
	HTTPStatus     int    `json:"http_status"`
	HTTPStatusText string `json:"http_status_text"`
	FetchElapsedMS int64  `json:"fetch_elapsed_ms"`
	ScreenshotOK   bool   `json:"screenshot_ok"`
	LinksFound     int    `json:"links_found"`
	Error          string `json:"error,omitempty"`
	TimestampUTC   string `json:"timestamp_utc"`
}

func main() {
	var (
		urlArg     = flag.String("url", "", "Tek bir URL çek (örn: https://example.com)")
		all        = flag.Bool("all", false, "Kod içindeki 15 URL'yi toplu çalıştır")
		outRoot    = flag.String("out", "output", "Çıktı klasörü")
		timeoutSec = flag.Int("timeout", 25, "Her site için timeout (saniye)")
		noShot     = flag.Bool("no-screenshot", false, "Ekran görüntüsü alma (sadece HTML+link)")
	)
	flag.Parse()

	// Ödev: URL komut satırı argümanı ile alınabilmeli.
	// -> -url verildiğinde tek hedef çalışır.
	// Kullanıcı "tek tek uğraşmak istemiyorum" dediği için,
	// -> -all ile kod içindeki 15 hedefi otomatik geziyoruz.
	var targets []string
	if *urlArg != "" {
		targets = []string{*urlArg}
	} else if *all || len(flag.Args()) == 0 {
		// default: -url verilmediyse ve başka arg da yoksa toplu çalıştır
		targets = append([]string{}, defaultTargets...)
	} else {
		// İstersen: go run . https://site.com gibi argüman da destekleyelim
		targets = append([]string{}, flag.Args()...)
	}

	if err := os.MkdirAll(*outRoot, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "[-] output klasörü oluşturulamadı: %v\n", err)
		os.Exit(2)
	}

	results := make([]Result, 0, len(targets))
	for i, t := range targets {
		fmt.Printf("\n[%d/%d] %s\n", i+1, len(targets), t)

		res := scrapeOne(t, *outRoot, time.Duration(*timeoutSec)*time.Second, !*noShot)
		results = append(results, res)

		if res.Error != "" {
			fmt.Printf("   [-] Hata: %s\n", res.Error)
		} else {
			fmt.Printf("   [+] HTML kaydedildi. Status=%d, Link=%d, Screenshot=%v\n",
				res.HTTPStatus, res.LinksFound, res.ScreenshotOK)
		}
	}

	// Toplu özet
	summaryPath := filepath.Join(*outRoot, "summary.json")
	_ = writeJSON(summaryPath, results)

	fmt.Printf("\n[+] Bitti. Özet: %s\n", summaryPath)
}

func scrapeOne(rawURL, outRoot string, perSiteTimeout time.Duration, doScreenshot bool) Result {
	start := time.Now().UTC()
	res := Result{
		URL:          rawURL,
		TimestampUTC: start.Format(time.RFC3339),
	}

	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		res.Error = "geçersiz URL"
		return res
	}

	siteSlug := makeSiteSlug(parsed.String())
	outDir := filepath.Join(outRoot, siteSlug)
	res.OutDir = outDir
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		res.Error = fmt.Sprintf("output klasörü oluşturulamadı: %v", err)
		return res
	}

	ctx, cancel := context.WithTimeout(context.Background(), perSiteTimeout)
	defer cancel()

	// 1) HTML çek
	fetchStart := time.Now()
	statusCode, statusText, body, err := fetchHTML(ctx, parsed.String())
	res.FetchElapsedMS = time.Since(fetchStart).Milliseconds()
	res.HTTPStatus = statusCode
	res.HTTPStatusText = statusText

	if err != nil {
		res.Error = err.Error()
		_ = writeJSON(filepath.Join(outDir, "meta.json"), res)
		return res
	}

	htmlPath := filepath.Join(outDir, "site_data.html")
	if err := os.WriteFile(htmlPath, body, 0o644); err != nil {
		res.Error = fmt.Sprintf("HTML yazılamadı: %v", err)
		_ = writeJSON(filepath.Join(outDir, "meta.json"), res)
		return res
	}

	// 2) Linkleri çıkar (ek puan)
	links := extractLinks(parsed, body)
	res.LinksFound = len(links)
	linksPath := filepath.Join(outDir, "links.txt")
	_ = os.WriteFile(linksPath, []byte(strings.Join(links, "\n")+"\n"), 0o644)

	// 3) Screenshot (chromedp)
	if doScreenshot {
		ssPath := filepath.Join(outDir, "screenshot.png")
		if err := takeScreenshot(ctx, parsed.String(), ssPath); err != nil {
			res.ScreenshotOK = false
			// Screenshot hata olsa bile HTML+link zaten kaydedildi; hata mesajını meta'ya yazalım
			res.Error = "screenshot alınamadı: " + err.Error()
		} else {
			res.ScreenshotOK = true
		}
	}

	_ = writeJSON(filepath.Join(outDir, "meta.json"), res)
	return res
}

func fetchHTML(ctx context.Context, target string) (int, string, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return 0, "", nil, fmt.Errorf("istek oluşturulamadı: %v", err)
	}

	// Basit User-Agent (bazı siteler boş UA sevmez)
	req.Header.Set("User-Agent", "webscraper-go/1.0 (+https://example.com)")

	client := &http.Client{
		Timeout: 20 * time.Second,
		// Redirect default olarak takip edilir; istersen sınır koyabilirsin.
	}

	resp, err := client.Do(req)
	if err != nil {
		return 0, "", nil, fmt.Errorf("bağlantı hatası: %v", err)
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, resp.Status, nil, fmt.Errorf("body okunamadı: %v", err)
	}

	// 404 vb durumları da kullanıcıya düzgün gösterelim
	if resp.StatusCode >= 400 {
		return resp.StatusCode, resp.Status, b, fmt.Errorf("HTTP hata: %s", resp.Status)
	}

	return resp.StatusCode, resp.Status, b, nil
}

func takeScreenshot(ctx context.Context, target, outPath string) error {
	// chromedp kendi context'ini istiyor; dış timeout ctx'ini de kullanacağız
	allocCtx, cancel := chromedp.NewExecAllocator(ctx,
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.Flag("headless", true),
			chromedp.Flag("disable-gpu", true),
			chromedp.Flag("no-sandbox", true),
			chromedp.Flag("disable-dev-shm-usage", true),
			// Stabilite için:
			chromedp.WindowSize(1366, 768),
		)...,
	)
	defer cancel()

	bctx, cancel2 := chromedp.NewContext(allocCtx)
	defer cancel2()

	var png []byte
	tasks := chromedp.Tasks{
		chromedp.Navigate(target),
		chromedp.WaitReady("body", chromedp.ByQuery),
		// FullScreenshot: sayfanın tamamını çeker
		chromedp.FullScreenshot(&png, 90),
	}

	if err := chromedp.Run(bctx, tasks); err != nil {
		return err
	}
	return os.WriteFile(outPath, png, 0o644)
}

func extractLinks(baseURL *url.URL, htmlBytes []byte) []string {
	// Çok basit bir "href" çekme: HTML parse etmiyoruz, ama pratikte iş görür.
	// İstersen later: golang.org/x/net/html ile token token parse edebilirsin.
	s := string(htmlBytes)
	found := make(map[string]struct{})

	// aşırı basit tarama: href="..."
	lower := strings.ToLower(s)
	idx := 0
	for {
		p := strings.Index(lower[idx:], "href=")
		if p < 0 {
			break
		}
		p = p + idx
		q := p + len("href=")
		if q >= len(s) {
			break
		}
		quote := s[q]
		if quote != '"' && quote != '\'' {
			idx = q
			continue
		}
		q++ // open quote sonrası
		end := strings.IndexByte(s[q:], quote)
		if end < 0 {
			break
		}
		rawHref := strings.TrimSpace(s[q : q+end])
		idx = q + end + 1

		if rawHref == "" || strings.HasPrefix(rawHref, "#") || strings.HasPrefix(strings.ToLower(rawHref), "javascript:") || strings.HasPrefix(strings.ToLower(rawHref), "mailto:") {
			continue
		}

		u, err := url.Parse(rawHref)
		if err != nil {
			continue
		}
		abs := baseURL.ResolveReference(u)
		abs.Fragment = "" // # kısmını at
		found[abs.String()] = struct{}{}
	}

	out := make([]string, 0, len(found))
	for k := range found {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func makeSiteSlug(u string) string {
	// host + kısa hash ile benzersiz klasör adı
	pu, err := url.Parse(u)
	host := "site"
	if err == nil && pu.Host != "" {
		host = pu.Host
	}
	host = sanitize(host)

	h := sha1.Sum([]byte(u))
	short := hex.EncodeToString(h[:])[:8]

	return fmt.Sprintf("%s_%s", host, short)
}

func sanitize(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('_')
		}
	}
	out := b.String()
	out = strings.Trim(out, "_")
	if out == "" {
		return "site"
	}
	return out
}

func writeJSON(path string, v any) error {
	b, _ := json.MarshalIndent(v, "", "  ")
	return os.WriteFile(path, b, 0o644)
}
