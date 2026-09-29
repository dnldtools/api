package r2

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Account struct {
	Name      string `json:"name"`
	AccountID string `json:"account_id"`
	AccessKey string `json:"access_key"`
	SecretKey string `json:"secret_key"`
	Bucket    string `json:"bucket"`
}

type Config struct {
	Accounts   []Account
	PresignTTL time.Duration
}

type SignedURL struct {
	URL        string
	Account    string
	Bucket     string
	ObjectKey  string
	ExpiresAt  time.Time
	TTLSeconds int64
}

type Uploaded struct {
	Account   string
	Bucket    string
	ObjectKey string
	Size      int64
}

type entry struct {
	account Account
	s3      *s3.Client
	presign *s3.PresignClient
}

type Manager struct {
	entries []entry
	ttl     time.Duration
	next    atomic.Uint64
}

var errNoAccounts = fmt.Errorf("r2: no accounts configured")

func NewManager(cfg Config) (*Manager, error) {
	if len(cfg.Accounts) == 0 {
		return nil, errNoAccounts
	}
	ttl := cfg.PresignTTL
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	m := &Manager{entries: make([]entry, 0, len(cfg.Accounts)), ttl: ttl}
	for _, acc := range cfg.Accounts {
		if acc.Name == "" || acc.AccountID == "" || acc.AccessKey == "" || acc.SecretKey == "" || acc.Bucket == "" {
			return nil, fmt.Errorf("r2: account %q is incomplete", acc.Name)
		}
		client := s3.NewFromConfig(m.baseConfig(acc), func(o *s3.Options) {
			o.UsePathStyle = true
		})
		m.entries = append(m.entries, entry{
			account: acc,
			s3:      client,
			presign: s3.NewPresignClient(client),
		})
	}
	return m, nil
}

func (m *Manager) baseConfig(acc Account) aws.Config {
	return aws.Config{
		Region:      "auto",
		Credentials: credentials.NewStaticCredentialsProvider(acc.AccessKey, acc.SecretKey, ""),
		BaseEndpoint: aws.String(
			fmt.Sprintf("https://%s.r2.cloudflarestorage.com", acc.AccountID),
		),
	}
}

func (m *Manager) Count() int {
	return len(m.entries)
}

func (m *Manager) Names() []string {
	out := make([]string, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, e.account.Name)
	}
	return out
}

func (m *Manager) indexByName(name string) (int, error) {
	for i, e := range m.entries {
		if e.account.Name == name {
			return i, nil
		}
	}
	return -1, fmt.Errorf("r2: account %q not found", name)
}

func (m *Manager) pick() entry {
	n := m.next.Add(1) - 1
	return m.entries[int(n%uint64(len(m.entries)))]
}

func (m *Manager) Presign(ctx context.Context, objectKey string) (*SignedURL, error) {
	if len(m.entries) == 0 {
		return nil, errNoAccounts
	}
	return m.presign(ctx, m.pick(), objectKey)
}

func (m *Manager) PresignOn(ctx context.Context, name, objectKey string) (*SignedURL, error) {
	i, err := m.indexByName(name)
	if err != nil {
		return nil, err
	}
	return m.presign(ctx, m.entries[i], objectKey)
}

func (m *Manager) presign(ctx context.Context, e entry, objectKey string) (*SignedURL, error) {
	req, err := e.presign.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(e.account.Bucket),
		Key:    aws.String(objectKey),
	}, func(o *s3.PresignOptions) {
		o.Expires = m.ttl
	})
	if err != nil {
		return nil, fmt.Errorf("r2: presign %s/%s: %w", e.account.Bucket, objectKey, err)
	}
	return &SignedURL{
		URL:        req.URL,
		Account:    e.account.Name,
		Bucket:     e.account.Bucket,
		ObjectKey:  objectKey,
		ExpiresAt:  time.Now().Add(m.ttl),
		TTLSeconds: int64(m.ttl.Seconds()),
	}, nil
}

func (m *Manager) Upload(ctx context.Context, objectKey string, body io.Reader, length int64, contentType string) (*Uploaded, error) {
	if len(m.entries) == 0 {
		return nil, errNoAccounts
	}
	return m.upload(ctx, m.pick(), objectKey, body, length, contentType)
}

func (m *Manager) UploadOn(ctx context.Context, name, objectKey string, body io.Reader, length int64, contentType string) (*Uploaded, error) {
	i, err := m.indexByName(name)
	if err != nil {
		return nil, err
	}
	return m.upload(ctx, m.entries[i], objectKey, body, length, contentType)
}

func (m *Manager) upload(ctx context.Context, e entry, objectKey string, body io.Reader, length int64, contentType string) (*Uploaded, error) {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	in := &s3.PutObjectInput{
		Bucket:      aws.String(e.account.Bucket),
		Key:         aws.String(objectKey),
		Body:        body,
		ContentType: aws.String(contentType),
	}
	if length >= 0 {
		in.ContentLength = aws.Int64(length)
	}
	if _, err := e.s3.PutObject(ctx, in); err != nil {
		return nil, fmt.Errorf("r2: upload %s/%s: %w", e.account.Bucket, objectKey, err)
	}
	return &Uploaded{
		Account:   e.account.Name,
		Bucket:    e.account.Bucket,
		ObjectKey: objectKey,
		Size:      length,
	}, nil
}
