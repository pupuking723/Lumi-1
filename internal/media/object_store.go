package media

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
)

const (
	defaultAliyunOSSRegion       = "cn-hangzhou"
	defaultAliyunOSSPrefix       = "chat-media/"
	defaultAliyunOSSSignedURLTTL = time.Hour
)

// ObjectStore stores chat media in OSS through Aliyun's S3-compatible API.
type ObjectStore struct {
	client        *s3.Client
	presignClient *s3.PresignClient
	bucket        string
	prefix        string
	publicBaseURL string
	signedURLTTL  time.Duration
}

type ObjectStoreConfig struct {
	AccessKeyID     string
	AccessKeySecret string
	Bucket          string
	Region          string
	Endpoint        string
	Prefix          string
	PublicBaseURL   string
	SignedURLTTL    time.Duration
	UsePathStyle    bool
}

func ObjectStorageEnabledFromEnv() bool {
	backend := strings.ToLower(strings.TrimSpace(os.Getenv("GOCLAW_MEDIA_STORAGE_BACKEND")))
	return backend == "oss" || backend == "aliyun_oss"
}

func NewObjectStoreFromEnv() (*ObjectStore, error) {
	if !ObjectStorageEnabledFromEnv() {
		return nil, nil
	}
	region := strings.TrimSpace(os.Getenv("GOCLAW_ALIYUN_OSS_REGION"))
	if region == "" {
		region = defaultAliyunOSSRegion
	}
	endpoint := strings.TrimSpace(os.Getenv("GOCLAW_ALIYUN_OSS_ENDPOINT"))
	if endpoint == "" {
		endpoint = fmt.Sprintf("https://s3.oss-%s.aliyuncs.com", region)
	}
	ttl := defaultAliyunOSSSignedURLTTL
	if raw := strings.TrimSpace(os.Getenv("GOCLAW_ALIYUN_OSS_SIGNED_URL_TTL")); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("parse GOCLAW_ALIYUN_OSS_SIGNED_URL_TTL: %w", err)
		}
		ttl = parsed
	}
	return NewObjectStore(ObjectStoreConfig{
		AccessKeyID:     strings.TrimSpace(os.Getenv("GOCLAW_ALIYUN_OSS_ACCESS_KEY_ID")),
		AccessKeySecret: strings.TrimSpace(os.Getenv("GOCLAW_ALIYUN_OSS_ACCESS_KEY_SECRET")),
		Bucket:          strings.TrimSpace(os.Getenv("GOCLAW_ALIYUN_OSS_BUCKET")),
		Region:          region,
		Endpoint:        endpoint,
		Prefix:          strings.TrimSpace(os.Getenv("GOCLAW_ALIYUN_OSS_PREFIX")),
		PublicBaseURL:   strings.TrimSpace(os.Getenv("GOCLAW_ALIYUN_OSS_PUBLIC_BASE_URL")),
		SignedURLTTL:    ttl,
		UsePathStyle:    parseObjectStoreBool(os.Getenv("GOCLAW_ALIYUN_OSS_USE_PATH_STYLE")),
	})
}

func NewObjectStore(cfg ObjectStoreConfig) (*ObjectStore, error) {
	if strings.TrimSpace(cfg.AccessKeyID) == "" {
		return nil, fmt.Errorf("GOCLAW_ALIYUN_OSS_ACCESS_KEY_ID is required")
	}
	if strings.TrimSpace(cfg.AccessKeySecret) == "" {
		return nil, fmt.Errorf("GOCLAW_ALIYUN_OSS_ACCESS_KEY_SECRET is required")
	}
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, fmt.Errorf("GOCLAW_ALIYUN_OSS_BUCKET is required")
	}
	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = defaultAliyunOSSRegion
	}
	prefix := strings.Trim(strings.TrimSpace(cfg.Prefix), "/")
	if prefix == "" {
		prefix = strings.TrimSuffix(defaultAliyunOSSPrefix, "/")
	}
	ttl := cfg.SignedURLTTL
	if ttl <= 0 {
		ttl = defaultAliyunOSSSignedURLTTL
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(cfg.AccessKeyID, cfg.AccessKeySecret, "")),
		awsconfig.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenRequired),
	)
	if err != nil {
		return nil, fmt.Errorf("load oss config: %w", err)
	}
	var s3Opts []func(*s3.Options)
	if endpoint := strings.TrimSpace(cfg.Endpoint); endpoint != "" {
		s3Opts = append(s3Opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(endpoint)
			o.UsePathStyle = cfg.UsePathStyle
		})
	}
	client := s3.NewFromConfig(awsCfg, s3Opts...)
	return &ObjectStore{
		client:        client,
		presignClient: s3.NewPresignClient(client),
		bucket:        strings.TrimSpace(cfg.Bucket),
		prefix:        prefix,
		publicBaseURL: strings.TrimRight(strings.TrimSpace(cfg.PublicBaseURL), "/"),
		signedURLTTL:  ttl,
	}, nil
}

func (s *ObjectStore) Bucket() string {
	if s == nil {
		return ""
	}
	return s.bucket
}

func (s *ObjectStore) ObjectKey(tenantID uuid.UUID, userID, sessionID, mediaID, ext string) string {
	if ext == "" {
		ext = ".bin"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	tenantPart := tenantID.String()
	if tenantID == uuid.Nil {
		tenantPart = "master"
	}
	parts := []string{
		strings.Trim(s.prefix, "/"),
		"tenants", tenantPart,
		"users", shortObjectKeyHash(userID),
		"sessions", shortObjectKeyHash(sessionID),
		strings.TrimSpace(mediaID) + ext,
	}
	return path.Join(parts...)
}

func (s *ObjectStore) ArtifactKey(tenantID uuid.UUID, agentKey, artifactID, ext string) string {
	if ext == "" {
		ext = ".bin"
	}
	if !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}
	tenantPart := tenantID.String()
	if tenantID == uuid.Nil {
		tenantPart = "master"
	}
	return path.Join(
		strings.Trim(s.prefix, "/"),
		"tenants", tenantPart,
		"artifacts", shortObjectKeyHash(agentKey),
		strings.TrimSpace(artifactID)+ext,
	)
}

func (s *ObjectStore) UploadFile(ctx context.Context, key, filePath, mimeType string, size int64) error {
	if s == nil {
		return fmt.Errorf("oss object store is not configured")
	}
	f, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("open upload file: %w", err)
	}
	defer f.Close()
	uploader := manager.NewUploader(s.client, func(u *manager.Uploader) {
		u.PartSize = 10 << 20
		u.Concurrency = 3
	})
	input := &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(key),
		Body:          f,
		ContentLength: aws.Int64(size),
	}
	if strings.TrimSpace(mimeType) != "" {
		input.ContentType = aws.String(mimeType)
	}
	if _, err := uploader.Upload(ctx, input); err != nil {
		return fmt.Errorf("oss upload %q: %w", key, err)
	}
	return nil
}

func (s *ObjectStore) DownloadToFile(ctx context.Context, key, dstPath string) error {
	if s == nil {
		return fmt.Errorf("oss object store is not configured")
	}
	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return fmt.Errorf("oss download %q: %w", key, err)
	}
	defer resp.Body.Close()
	out, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("create download file: %w", err)
	}
	defer out.Close()
	if _, err := io.Copy(out, resp.Body); err != nil {
		return fmt.Errorf("write download file: %w", err)
	}
	return nil
}

func (s *ObjectStore) ReadFile(ctx context.Context, key string) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("oss object store is not configured")
	}
	resp, err := s.client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, fmt.Errorf("oss read %q: %w", key, err)
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

func (s *ObjectStore) URL(ctx context.Context, key string) (string, error) {
	if s == nil || strings.TrimSpace(key) == "" {
		return "", nil
	}
	if s.publicBaseURL != "" {
		base, err := url.Parse(s.publicBaseURL)
		if err != nil {
			return "", fmt.Errorf("parse oss public base URL: %w", err)
		}
		base.Path = path.Join(base.Path, strings.TrimLeft(key, "/"))
		return base.String(), nil
	}
	req, err := s.presignClient.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(s.bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(s.signedURLTTL))
	if err != nil {
		return "", fmt.Errorf("presign oss object %q: %w", key, err)
	}
	return req.URL, nil
}

func parseObjectStoreBool(raw string) bool {
	v, err := strconv.ParseBool(strings.TrimSpace(raw))
	return err == nil && v
}

func shortObjectKeyHash(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "default"
	}
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:6])
}
