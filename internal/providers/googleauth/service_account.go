package googleauth

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	CloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"
	defaultTokenURI    = "https://oauth2.googleapis.com/token"
)

var (
	credentialFileEnvNames = []string{"GOOGLE_APPLICATION_CREDENTIALS", "VERTEX_SERVICE_ACCOUNT_FILE", "GOCLAW_VERTEX_SERVICE_ACCOUNT_FILE"}
	credentialJSONEnvNames = []string{"GOOGLE_APPLICATION_CREDENTIALS_JSON", "VERTEX_SERVICE_ACCOUNT_JSON", "GOCLAW_VERTEX_SERVICE_ACCOUNT_JSON"}
)

type serviceAccountCredentials struct {
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

type ServiceAccountTokenSource struct {
	credentials serviceAccountCredentials
	scope       string
	httpClient  *http.Client

	mu     sync.Mutex
	token  string
	expiry time.Time
}

func NewServiceAccountTokenSourceFromEnv(httpClient *http.Client) (*ServiceAccountTokenSource, bool, error) {
	rawJSON := ""
	for _, key := range credentialJSONEnvNames {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			rawJSON = value
			break
		}
	}
	if rawJSON == "" {
		for _, key := range credentialFileEnvNames {
			if path := strings.TrimSpace(os.Getenv(key)); path != "" {
				data, err := os.ReadFile(path)
				if err != nil {
					return nil, true, fmt.Errorf("read %s: %w", key, err)
				}
				rawJSON = string(data)
				break
			}
		}
	}
	if strings.TrimSpace(rawJSON) == "" {
		return nil, false, nil
	}
	source, err := NewServiceAccountTokenSource([]byte(rawJSON), CloudPlatformScope, httpClient)
	if err != nil {
		return nil, true, err
	}
	return source, true, nil
}

func NewServiceAccountTokenSource(raw []byte, scope string, httpClient *http.Client) (*ServiceAccountTokenSource, error) {
	var credentials serviceAccountCredentials
	if err := json.Unmarshal(raw, &credentials); err != nil {
		return nil, fmt.Errorf("parse service account credentials: %w", err)
	}
	credentials.ClientEmail = strings.TrimSpace(credentials.ClientEmail)
	credentials.PrivateKey = strings.TrimSpace(credentials.PrivateKey)
	credentials.TokenURI = strings.TrimSpace(credentials.TokenURI)
	if credentials.ClientEmail == "" || credentials.PrivateKey == "" {
		return nil, fmt.Errorf("service account credentials require client_email and private_key")
	}
	if credentials.TokenURI == "" {
		credentials.TokenURI = defaultTokenURI
	}
	if strings.TrimSpace(scope) == "" {
		scope = CloudPlatformScope
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &ServiceAccountTokenSource{credentials: credentials, scope: scope, httpClient: httpClient}, nil
}

func (s *ServiceAccountTokenSource) AccessToken(ctx context.Context) (string, error) {
	if s == nil {
		return "", fmt.Errorf("service account token source is nil")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.token != "" && time.Until(s.expiry) > 5*time.Minute {
		return s.token, nil
	}
	token, expiry, err := s.refreshLocked(ctx)
	if err != nil {
		return "", err
	}
	s.token = token
	s.expiry = expiry
	return token, nil
}

func (s *ServiceAccountTokenSource) refreshLocked(ctx context.Context) (string, time.Time, error) {
	assertion, err := s.jwtAssertion(time.Now())
	if err != nil {
		return "", time.Time{}, err
	}
	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", assertion)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.credentials.TokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= http.StatusBadRequest {
		return "", time.Time{}, fmt.Errorf("service account token request failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var parsed tokenResponse
	if err := json.NewDecoder(bytes.NewReader(body)).Decode(&parsed); err != nil {
		return "", time.Time{}, fmt.Errorf("parse service account token response: %w", err)
	}
	parsed.AccessToken = strings.TrimSpace(parsed.AccessToken)
	if parsed.AccessToken == "" {
		return "", time.Time{}, fmt.Errorf("service account token response did not include access_token")
	}
	expiresIn := parsed.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	return parsed.AccessToken, time.Now().Add(time.Duration(expiresIn) * time.Second), nil
}

func (s *ServiceAccountTokenSource) jwtAssertion(now time.Time) (string, error) {
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iss":   s.credentials.ClientEmail,
		"scope": s.scope,
		"aud":   s.credentials.TokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	claimsJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	key, err := parseRSAPrivateKey(s.credentials.PrivateKey)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign service account assertion: %w", err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func parseRSAPrivateKey(value string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(value))
	if block == nil {
		return nil, fmt.Errorf("private_key is not PEM encoded")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private_key is not an RSA private key")
		}
		return rsaKey, nil
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("parse RSA private_key")
}

func GcloudAccessToken(ctx context.Context) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gcloud", "auth", "print-access-token").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func AccessTokenFromEnvOrGcloud(ctx context.Context, httpClient *http.Client) (string, error) {
	if source, ok, err := NewServiceAccountTokenSourceFromEnv(httpClient); err != nil {
		return "", err
	} else if ok {
		return source.AccessToken(ctx)
	}
	return GcloudAccessToken(ctx)
}
