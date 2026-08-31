# rest-api

REST API untuk mengunduh media dari berbagai platform (saat ini Facebook,
Instagram, TikTok, Shopee, dan YouTube; platform lain menyusul).

> **Status:** Facebook sudah terimplementasi (via fget.io), Instagram
> (official API relay + snapinsta fallback), TikTok (snaptik
> fallback utama + official rehydration relay), Shopee (official
> watermark + shopeenowatermark lewat proxy), dan YouTube (search + format
> catalog + konversi via convert1s). Platform lain ditambahkan sesuai scraper
> yang dikirim kemudian.

---

## Fitur

- Struktur project modular yang rapi dan mudah dikembangkan.
- Engine `downloader` (milik rest-api) terpisah dari adapter platform
  (`downloader/providers`); domain tidak bergantung pada HTTP/status code/JSON.
- Kontrak domain downloader (Stage 9): `Request → Service → PlatformResolver →
  Provider → Result`, provider type (`native`/`browser`/`external_api`),
  auto-deteksi platform via `URLMatcher`, dan port browser (`BrowserRuntime`)
  tanpa mengimpor Playwright. Lihat [DOWNLOADER.md](docs/DOWNLOADER.md) dan
  [PROVIDERS.md](docs/PROVIDERS.md).
- Browser runtime & provider lifecycle (Stage 10): `internal/browser.Runtime`
  sebagai adapter `downloader.BrowserRuntime`, injeksi runtime ke provider
  `BrowserCapable`, dan lifecycle provider (`Init`/`Ready`/`Shutdown`) yang
  best-effort. Infrastruktur browser siap, tetapi scraping/unduhan **belum**
  diimplementasikan.
- REST API **full JSON** dengan schema response & error yang konsisten.
- Opsi **pretty / indented JSON** untuk development.
- Fitur YouTube: pencarian, katalog format, dan konversi/download via convert1s
  (lihat [YOUTUBE.md](docs/YOUTUBE.md)).
- Konfigurasi via environment variable + `.env`.
- Lapisan browser automation (Playwright) yang terabstraksi dan **opsional**
  (nonaktif secara default; lihat [BROWSER.md](docs/BROWSER.md)), termasuk
  `browser.Runtime` sebagai adapter port `downloader.BrowserRuntime`.
- Infrastruktur **PostgreSQL** (primary database & source of truth metrics) dan
  **Redis** (counter/aggregasi best-effort) yang opsional.
- Sistem **metrics API** (total request, sukses/gagal, API key valid/invalid/
  missing, status, endpoint, platform, durasi, timestamp, rate-limited,
  quota-exceeded) dengan success rate yang dihitung konsisten dari satu definisi.
- **Autentikasi API key** (Stage 7): akun + API key berbasis database (key
  disimpan sebagai SHA-256 hash), rate limit per-plan, dan kuota harian/bulanan
  per-plan. Lihat [AUTH.md](docs/AUTH.md), [PLANS.md](docs/PLANS.md), dan
  [RATE_LIMIT.md](docs/RATE_LIMIT.md).
- Kontrak REST API **OpenAPI 3.0** (`docs/openapi.yaml`) sebagai source of
  truth endpoint (method, path, header, body, status, error) + contract test
  yang menjaga sinkron dengan route yang diimplementasikan.
- Dependency pihak ketiga terisolasi: HTTP/downloader memakai stdlib; adapter
  browser (`playwright-go`), database (`pgx`), dan cache (`go-redis`) terpisah.

## Struktur Project

```
rest-api/
├── cmd/
│   ├── api/                 # Entry point aplikasi
│   └── devkey/              # CLI provisioning akun + API key dev (cetak raw key sekali)
├── internal/
│   ├── app/                # Application bootstrap / composition root (wiring + lifecycle)
│   ├── config/              # Config loader
│   ├── errors/              # Centralized application error system
│   ├── http/                # Router, server, middleware, handler, response & error helper
│   ├── auth/                # API-key authentication (akun, key, service, repo)
│   ├── plans/               # Definisi plan & policy (rate limit + kuota)
│   ├── ratelimit/           # Rate limiter jendela-pendek (Redis + in-memory)
│   ├── quota/               # Kuota harian/bulanan (Postgres source of truth)
│   ├── downloader/          # Domain + engine (types, Provider/Resolver interface, Registry, Service)
│   │   └── providers/       # Adapter platform (facebook, instagram, tiktok, shopee)
│   ├── youtube/             # Layanan YouTube (search, formats, convert) via convert1s
│   ├── browser/             # Abstraksi browser automation + adapter Playwright
│   ├── database/            # PostgreSQL: connection pool + migration runner
│   ├── cache/               # Redis: client (counter best-effort)
│   └── metrics/             # API metrics (Event, Stats, Service, Repository)
│   └── testutil/            # Helper test: DB/Redis terisolasi + fixture (test-only)
├── pkg/
│   └── env/                 # Helper environment (.env) yang reusable
├── docs/                    # Dokumentasi (PRD, Architecture, Application, API, Flow, Development, Browser, Errors, Database, Metrics, Configuration, Changelog) + openapi.yaml
├── .env.example
├── Makefile
└── go.mod
```

## Persyaratan

- Go **1.25+** (menggunakan method-based routing bawaan `net/http`; toolchain
  minimum mengikuti dependensi `pgx`).
- (Opsional) Browser automation: `go mod tidy` + `playwright install chromium`,
  hanya jika `BROWSER_ENABLED=true`. Lihat [BROWSER.md](docs/BROWSER.md).
- (Opsional) PostgreSQL untuk persistence metrics dan Redis untuk counter
  real-time. Lihat [DATABASE.md](docs/DATABASE.md) dan [METRICS.md](docs/METRICS.md).

## Quick Start

```bash
# 1. Salin konfigurasi
cp .env.example .env

# 2. Jalankan
make run
# atau
go run ./cmd/api

# 3. (Opsional) Buat akun + API key dev untuk endpoint terproteksi
go run ./cmd/devkey -name dev -plan pro -key-name dev-cli

# 4. Coba endpoint
curl -X POST http://localhost:8080/v1/downloads \
     -H "Content-Type: application/json" \
     -H "X-API-Key: <raw key dari devkey>" \
     -d '{"platform":"facebook","url":"https://www.facebook.com/reel/123456"}'

# Cari video YouTube (butuh API key)
curl "http://localhost:8080/v1/youtube/search?q=lofi" \
     -H "X-API-Key: <raw key dari devkey>"
```

> Seluruh endpoint `/v1/...` mewajibkan `X-API-Key`. Tanpa key → 401, akun
> suspended/disabled → 403, rate limit/kuota habis → 429.

## Endpoint

| Method | Path                  | Status      | Auth                | Deskripsi                              |
| ------ | --------------------- | ----------- | ------------------- | -------------------------------------- |
| GET    | `/v1/account`         | implemented | `X-API-Key` wajib   | Info akun                              |
| GET    | `/v1/usage`           | implemented | `X-API-Key` wajib   | Plan, rate limit, kuota terpakai       |
| GET    | `/v1/keys`            | implemented | `X-API-Key` wajib   | Daftar API key                         |
| POST   | `/v1/keys`            | implemented | `X-API-Key` wajib   | Buat API key baru                      |
| POST   | `/v1/keys/{id}/revoke`| implemented | `X-API-Key` wajib   | Cabut API key                          |
| GET    | `/v1/admin/accounts`  | implemented | `X-API-Key` + role `admin` | Daftar semua akun              |
| GET    | `/v1/admin/accounts/{id}` | implemented | `X-API-Key` + role `admin` | Detail akun               |
| PATCH  | `/v1/admin/accounts/{id}` | implemented | `X-API-Key` + role `admin` | Ubah akun (soft)         |
| DELETE | `/v1/admin/accounts/{id}` | implemented | `X-API-Key` + role `admin` | Nonaktifkan akun (soft-delete) |
| GET    | `/v1/admin/keys`      | implemented | `X-API-Key` + role `admin` | Daftar semua key lintas akun |
| GET    | `/v1/admin/keys/{id}` | implemented | `X-API-Key` + role `admin` | Detail key               |
| POST   | `/v1/admin/keys`      | implemented | `X-API-Key` + role `admin` | Buat key untuk akun lain (raw key sekali) |
| PATCH  | `/v1/admin/keys/{id}` | implemented | `X-API-Key` + role `admin` | Ubah key (name/status)  |
| DELETE | `/v1/admin/keys/{id}` | implemented | `X-API-Key` + role `admin` | Cabut key (revoke)      |
| GET    | `/v1/admin/stats`     | implemented | `X-API-Key` + role `admin` | Statistik platform      |
| POST   | `/v1/downloads`       | skeleton    | `X-API-Key` wajib   | Resolve/download media (Facebook via fget.io, Instagram official + snapinsta, TikTok snaptik + official, Shopee official + shopeenowatermark) |
| GET    | `/v1/youtube/search`  | implemented | `X-API-Key` wajib   | Cari video YouTube (param `q`)         |
| GET    | `/v1/youtube/formats` | implemented | `X-API-Key` wajib   | Katalog format konversi YouTube        |
| POST   | `/v1/youtube/convert` | implemented | `X-API-Key` wajib   | Konversi/download video YouTube (kuota) |

Semua response (termasuk 404) selalu dalam format JSON.

## Pretty JSON

Aktifkan pretty JSON secara global:

```env
PRETTY_JSON=true
```

atau per-request dengan query parameter:

```bash
curl "http://localhost:8080/v1/account?pretty=1" -H "X-API-Key: <key>"
```

## Dokumentasi

- [PRD](docs/PRD.md) — Product Requirements Document
- [Architecture](docs/ARCHITECTURE.md) — Arsitektur & aturan dependency
- [Application](docs/APPLICATION.md) — Bootstrap, wiring, server lifecycle, middleware
- [API](docs/API.md) — Kontrak API & schema response/error
- [Downloader](docs/DOWNLOADER.md) — Kontrak domain downloader (request/result/interface/alur)
- [Providers](docs/PROVIDERS.md) — Arsitektur provider (type, capability, registrasi, implementasi)
- [YouTube](docs/YOUTUBE.md) — Layanan YouTube (search, formats, convert via convert1s)
- [OpenAPI](docs/openapi.yaml) — Spesifikasi OpenAPI 3.0 (source of truth kontrak)
- [Flow](docs/FLOW.md) — Alur request & data
- [Auth](docs/AUTH.md) — Autentikasi API key, akun, dan hash key
- [Plans](docs/PLANS.md) — Definisi plan & policy (rate limit + kuota)
- [Rate Limit](docs/RATE_LIMIT.md) — Rate limiting & kuota (konsep & implementasi)
- [Browser](docs/BROWSER.md) — Browser automation layer
- [Errors](docs/ERRORS.md) — Centralized error handling system
- [Database](docs/DATABASE.md) — PostgreSQL, skema, migrations, pool
- [Metrics](docs/METRICS.md) — Sistem metrics API & definisi success rate
- [Configuration](docs/CONFIGURATION.md) — Referensi semua environment variable
- [Development](docs/DEVELOPMENT.md) — Panduan pengembangan & menambah platform
- [Testing](docs/TESTING.md) — Strategi test, fixture, dan cara menjalankan (unit/integration/race)
- [Changelog](docs/CHANGELOG.md) — Catatan perubahan

## Development

```bash
make build             # build binary ke ./bin
make test              # unit test (integration di-skip tanpa TEST_*)
make test-integration  # unit + integration (PostgreSQL/Redis terisolasi)
make test-race         # race detector (butuh CGO + compiler C)
make vet               # go vet
make fmt               # gofmt
make fmtcheck          # cek format
make check             # fmtcheck + vet + test + build
make tidy              # go mod tidy
make clean             # hapus artifact build
```

Lihat [TESTING.md](docs/TESTING.md) untuk detail strategi test, konvensi test
PostgreSQL/Redis, dan batasan race detector.

## Catatan

Nama module saat ini adalah `rest-api`. Jika project akan di-publish, ubah module
path di `go.mod` menjadi path repository (mis. `github.com/username/rest-api`) dan
sesuaikan import path-nya.
