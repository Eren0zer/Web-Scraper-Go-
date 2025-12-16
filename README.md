# webscraper-go

Bu proje, verilen PDF ödevindeki gereksinimleri karşılayacak şekilde hazırlanmıştır:  
- URL'ye HTTP isteği atar, ham HTML'i dosyaya kaydeder  
- Hata kontrolü yapar (404 vb.)  
- Sayfanın ekran görüntüsünü alır (`screenshot.png`)  
- Ek puan: sayfadaki URL'leri listeler (`links.txt`)  

## Kurulum

1) Go kurulu olmalı (Go 1.22+ önerilir)  
2) Chrome/Chromium kurulu olmalı (chromedp bunu kullanır)

Bağımlılıkları indir:

```bash
go mod tidy
```

## Kullanım

### Tek bir site

```bash
go run . -url https://example.com -out output
```

Çıktı:
```
output/<site>_hash/
  site_data.html
  screenshot.png
  links.txt
  meta.json
```

### Kod içindeki 15 siteyi otomatik çalıştır (linkleri tek tek girme derdi yok)

```bash
go run . -all -out output
```

Özet:
- `output/summary.json` (tüm sitelerin durumlarını listeler)

### Screenshot istemiyorsan

```bash
go run . -all -no-screenshot
```

## Notlar

- Bazı siteler headless tarayıcıyı engelleyebilir. HTML çekilmiş olsa bile screenshot hata verebilir; program yine de HTML+linkleri kaydeder.
- URL listesini `main.go` içindeki `defaultTargets` dizisinden değiştirebilirsin.
