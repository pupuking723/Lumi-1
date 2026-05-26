package http

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/i18n"
	"github.com/nextlevelbuilder/goclaw/internal/permissions"
	"github.com/nextlevelbuilder/goclaw/internal/store"
	"github.com/nextlevelbuilder/goclaw/pkg/protocol"
)

const (
	googleAuthURL      = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL     = "https://oauth2.googleapis.com/token"
	googleUserInfoURL  = "https://www.googleapis.com/oauth2/v3/userinfo"
	googleCertsURL     = "https://www.googleapis.com/oauth2/v3/certs"
	googleScopeProfile = "https://www.googleapis.com/auth/userinfo.profile"
	googleScopeEmail   = "https://www.googleapis.com/auth/userinfo.email"

	defaultGoogleSessionTTL = 30 * 24 * time.Hour
)

type GoogleAuthHandler struct {
	tenantStore store.TenantStore
	msgBus      *bus.MessageBus
	provider    googleIdentityProvider
	cfg         googleAuthConfig
}

type googleAuthConfig struct {
	UserIDPrefix   string
	DefaultTenant  string
	DefaultRole    string
	SessionTTL     time.Duration
	AllowedDomains []string
}

type googleLoginRequest struct {
	Credential  string `json:"credential"`
	IDToken     string `json:"id_token"`
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
}

type googleLoginResponse struct {
	Authenticated bool               `json:"authenticated"`
	TokenType     string             `json:"token_type"`
	AccessToken   string             `json:"access_token"`
	ExpiresAt     time.Time          `json:"expires_at"`
	ExpiresIn     int64              `json:"expires_in"`
	User          googleUserResponse `json:"user"`
	Tenant        googleTenantInfo   `json:"tenant"`
}

type googleUserResponse struct {
	ID         string `json:"id"`
	Provider   string `json:"provider"`
	ProviderID string `json:"provider_id"`
	Name       string `json:"name,omitempty"`
	Email      string `json:"email,omitempty"`
	Avatar     string `json:"avatar,omitempty"`
}

type googleTenantInfo struct {
	ID   string `json:"id"`
	Slug string `json:"slug,omitempty"`
	Role string `json:"role"`
}

type googleIdentity struct {
	ProviderID string
	Name       string
	Email      string
	Avatar     string
}

type googleIdentityProvider interface {
	Authorize(context.Context, googleLoginRequest) (*googleIdentity, error)
}

type googleProvider struct {
	cfg       *oauth2.Config
	client    *http.Client
	keysURL   string
	userURL   string
	audiences []string
	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	keysUntil time.Time
	now       func() time.Time
}

func NewGoogleAuthHandler(tenantStore store.TenantStore, msgBus *bus.MessageBus) *GoogleAuthHandler {
	return &GoogleAuthHandler{
		tenantStore: tenantStore,
		msgBus:      msgBus,
		provider:    newGoogleProvider(googleAuthAudiences()),
		cfg:         googleAuthConfigFromEnv(),
	}
}

func (h *GoogleAuthHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /v1/auth/google/login", h.handleLogin)
	mux.HandleFunc("GET /v1/auth/me", requireAuth(permissions.RoleViewer, h.handleMe))
	mux.HandleFunc("POST /v1/auth/logout", requireAuth(permissions.RoleViewer, h.handleLogout))
}

func (h *GoogleAuthHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	locale := extractLocale(r)
	if h.tenantStore == nil {
		writeError(w, http.StatusServiceUnavailable, protocol.ErrInternal, "tenant store not available")
		return
	}
	if pkgUserSessionSigner == nil || !pkgUserSessionSigner.enabled() {
		writeError(w, http.StatusInternalServerError, protocol.ErrInternal, "Google auth session secret is not configured")
		return
	}

	var input googleLoginRequest
	if !bindJSON(w, r, locale, &input) {
		return
	}

	identity, err := h.provider.Authorize(r.Context(), input)
	if err != nil {
		slog.Warn("google_auth.login_failed", "error", err)
		writeError(w, http.StatusUnauthorized, protocol.ErrUnauthorized, i18n.T(locale, i18n.MsgUnauthorized))
		return
	}
	if identity == nil || identity.ProviderID == "" {
		writeError(w, http.StatusUnauthorized, protocol.ErrUnauthorized, i18n.T(locale, i18n.MsgUnauthorized))
		return
	}
	if !h.emailAllowed(identity.Email) {
		writeError(w, http.StatusForbidden, protocol.ErrUnauthorized, "Google account domain is not allowed")
		return
	}

	userID := h.userID(identity.ProviderID)
	if err := store.ValidateUserID(userID); err != nil {
		writeError(w, http.StatusBadRequest, protocol.ErrInvalidRequest, "Google user id is too long")
		return
	}

	tenant, err := h.resolveLoginTenant(r.Context())
	if err != nil {
		slog.Error("google_auth.resolve_tenant_failed", "error", err)
		writeError(w, http.StatusInternalServerError, protocol.ErrInternal, "failed to resolve login tenant")
		return
	}
	if tenant == nil {
		writeError(w, http.StatusInternalServerError, protocol.ErrInternal, "Google auth tenant not found")
		return
	}

	displayName := strings.TrimSpace(identity.Name)
	if displayName == "" {
		displayName = identity.Email
	}
	member, err := h.tenantStore.CreateTenantUserReturning(r.Context(), tenant.ID, userID, displayName, h.defaultRole())
	if err != nil {
		slog.Error("google_auth.upsert_user_failed", "error", err, "tenant_id", tenant.ID, "user_id", userID)
		writeError(w, http.StatusInternalServerError, protocol.ErrInternal, "failed to create user")
		return
	}
	h.emitCacheInvalidate(userID)

	role := permissionRoleFromTenantRole(member.Role)
	expiresAt := time.Now().Add(h.sessionTTL())
	token, err := pkgUserSessionSigner.Sign(userSessionClaims{
		UserID:     userID,
		TenantID:   tenant.ID,
		Role:       role,
		Name:       identity.Name,
		Email:      identity.Email,
		Avatar:     identity.Avatar,
		IssuedAt:   time.Now(),
		ExpiresAt:  expiresAt,
		Provider:   "google",
		ProviderID: identity.ProviderID,
	})
	if err != nil {
		slog.Error("google_auth.sign_session_failed", "error", err)
		writeError(w, http.StatusInternalServerError, protocol.ErrInternal, "failed to create session")
		return
	}

	writeJSON(w, http.StatusOK, googleLoginResponse{
		Authenticated: true,
		TokenType:     "Bearer",
		AccessToken:   token,
		ExpiresAt:     expiresAt.UTC(),
		ExpiresIn:     int64(time.Until(expiresAt).Seconds()),
		User: googleUserResponse{
			ID:         userID,
			Provider:   "google",
			ProviderID: identity.ProviderID,
			Name:       identity.Name,
			Email:      identity.Email,
			Avatar:     identity.Avatar,
		},
		Tenant: googleTenantInfo{
			ID:   tenant.ID.String(),
			Slug: tenant.Slug,
			Role: member.Role,
		},
	})
}

func (h *GoogleAuthHandler) handleMe(w http.ResponseWriter, r *http.Request) {
	userID := store.UserIDFromContext(r.Context())
	tenantID := store.TenantIDFromContext(r.Context())
	role := store.RoleFromContext(r.Context())
	resp := map[string]any{
		"authenticated": userID != "" || role != "",
		"user": map[string]any{
			"id": userID,
		},
		"tenant": map[string]any{
			"id":   tenantID.String(),
			"slug": store.TenantSlugFromContext(r.Context()),
			"role": role,
		},
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *GoogleAuthHandler) handleLogout(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}

func (h *GoogleAuthHandler) resolveLoginTenant(ctx context.Context) (*store.TenantData, error) {
	raw := strings.TrimSpace(h.cfg.DefaultTenant)
	if raw == "" || raw == "master" || raw == "default" {
		return h.tenantStore.GetTenant(ctx, store.MasterTenantID)
	}
	if id, err := uuid.Parse(raw); err == nil {
		return h.tenantStore.GetTenant(ctx, id)
	}
	return h.tenantStore.GetTenantBySlug(ctx, raw)
}

func (h *GoogleAuthHandler) defaultRole() string {
	role := strings.TrimSpace(h.cfg.DefaultRole)
	switch role {
	case store.TenantRoleOwner, store.TenantRoleAdmin, store.TenantRoleOperator, store.TenantRoleMember, store.TenantRoleViewer:
		return role
	default:
		return store.TenantRoleOperator
	}
}

func (h *GoogleAuthHandler) userID(providerID string) string {
	prefix := strings.Trim(strings.TrimSpace(h.cfg.UserIDPrefix), ":")
	if prefix == "" {
		prefix = "google"
	}
	return prefix + ":" + strings.TrimSpace(providerID)
}

func (h *GoogleAuthHandler) sessionTTL() time.Duration {
	if h.cfg.SessionTTL > 0 {
		return h.cfg.SessionTTL
	}
	return defaultGoogleSessionTTL
}

func (h *GoogleAuthHandler) emailAllowed(email string) bool {
	if len(h.cfg.AllowedDomains) == 0 {
		return true
	}
	_, domain, ok := strings.Cut(strings.ToLower(strings.TrimSpace(email)), "@")
	if !ok || domain == "" {
		return false
	}
	return slices.Contains(h.cfg.AllowedDomains, domain)
}

func (h *GoogleAuthHandler) emitCacheInvalidate(userID string) {
	if h.msgBus == nil {
		return
	}
	h.msgBus.Broadcast(bus.Event{
		Name:    protocol.EventCacheInvalidate,
		Payload: bus.CacheInvalidatePayload{Kind: bus.CacheKindTenantUsers, Key: userID},
	})
}

func newGoogleProvider(audiences []string) *googleProvider {
	return &googleProvider{
		cfg: &oauth2.Config{
			Endpoint: oauth2.Endpoint{
				AuthURL:   googleAuthURL,
				TokenURL:  googleTokenURL,
				AuthStyle: oauth2.AuthStyleInParams,
			},
			Scopes: []string{googleScopeProfile, googleScopeEmail},
		},
		client:    http.DefaultClient,
		keysURL:   googleCertsURL,
		userURL:   googleUserInfoURL,
		audiences: audiences,
		now:       time.Now,
	}
}

func (g *googleProvider) Authorize(ctx context.Context, req googleLoginRequest) (*googleIdentity, error) {
	if token := googleFirstNonEmpty(req.Credential, req.IDToken); token != "" {
		return g.authorizeIDToken(ctx, token)
	}
	if token := googleFirstNonEmpty(req.AccessToken, req.Token); token != "" {
		return g.authorizeAccessToken(ctx, token)
	}
	return nil, errors.New("missing Google credential or access token")
}

func (g *googleProvider) authorizeAccessToken(ctx context.Context, accessToken string) (*googleIdentity, error) {
	if g.client != nil {
		ctx = context.WithValue(ctx, oauth2.HTTPClient, g.client)
	}
	res, err := g.cfg.Client(ctx, &oauth2.Token{AccessToken: accessToken, TokenType: "Bearer"}).Get(g.userURL)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return nil, fmt.Errorf("google userinfo failed: status=%d body=%s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	var u struct {
		Sub     string `json:"sub"`
		Name    string `json:"name"`
		Email   string `json:"email"`
		Picture string `json:"picture"`
	}
	if err := json.NewDecoder(res.Body).Decode(&u); err != nil {
		return nil, err
	}
	return &googleIdentity{ProviderID: u.Sub, Name: u.Name, Email: u.Email, Avatar: u.Picture}, nil
}

func (g *googleProvider) authorizeIDToken(ctx context.Context, idToken string) (*googleIdentity, error) {
	claims, err := g.decodeIDToken(ctx, idToken)
	if err != nil {
		return nil, err
	}
	return &googleIdentity{
		ProviderID: claims.Subject,
		Name:       claims.Name,
		Email:      claims.Email,
		Avatar:     claims.Picture,
	}, nil
}

type googleIDTokenClaims struct {
	Issuer        string          `json:"iss"`
	Subject       string          `json:"sub"`
	Audience      json.RawMessage `json:"aud"`
	ExpiresAt     int64           `json:"exp"`
	IssuedAt      int64           `json:"iat"`
	Email         string          `json:"email"`
	EmailVerified bool            `json:"email_verified"`
	Name          string          `json:"name"`
	Picture       string          `json:"picture"`
}

func (g *googleProvider) decodeIDToken(ctx context.Context, idToken string) (*googleIDTokenClaims, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid Google id token")
	}
	headerBytes, err := jwtDecode(parts[0])
	if err != nil {
		return nil, err
	}
	var header struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return nil, err
	}
	if header.Alg != "RS256" || header.Kid == "" {
		return nil, errors.New("unsupported Google id token header")
	}
	key, err := g.publicKey(ctx, header.Kid)
	if err != nil {
		return nil, err
	}
	sig, err := jwtDecode(parts[2])
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], sig); err != nil {
		return nil, err
	}

	payloadBytes, err := jwtDecode(parts[1])
	if err != nil {
		return nil, err
	}
	var claims googleIDTokenClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return nil, err
	}
	if err := g.validateClaims(&claims); err != nil {
		return nil, err
	}
	return &claims, nil
}

func (g *googleProvider) validateClaims(claims *googleIDTokenClaims) error {
	now := g.now().Unix()
	if claims.Subject == "" {
		return errors.New("Google id token missing subject")
	}
	if claims.ExpiresAt <= now {
		return errors.New("Google id token expired")
	}
	if claims.Issuer != "accounts.google.com" && claims.Issuer != "https://accounts.google.com" {
		return errors.New("invalid Google id token issuer")
	}
	if len(g.audiences) == 0 {
		return errors.New("GOCLAW_GOOGLE_CLIENT_ID is required for Google id token login")
	}
	auds, err := parseJWTAudience(claims.Audience)
	if err != nil {
		return err
	}
	for _, aud := range auds {
		if slices.Contains(g.audiences, aud) {
			return nil
		}
	}
	return errors.New("invalid Google id token audience")
}

func (g *googleProvider) publicKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	g.mu.Lock()
	if g.keys != nil && time.Now().Before(g.keysUntil) {
		if key := g.keys[kid]; key != nil {
			g.mu.Unlock()
			return key, nil
		}
	}
	g.mu.Unlock()

	if err := g.refreshKeys(ctx); err != nil {
		return nil, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if key := g.keys[kid]; key != nil {
		return key, nil
	}
	return nil, errors.New("Google signing key not found")
}

func (g *googleProvider) refreshKeys(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.keysURL, nil)
	if err != nil {
		return err
	}
	res, err := g.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("google certs failed: status=%d", res.StatusCode)
	}
	var body struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			Alg string `json:"alg"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return err
	}
	keys := make(map[string]*rsa.PublicKey, len(body.Keys))
	for _, k := range body.Keys {
		if k.Kid == "" || k.Kty != "RSA" || k.Alg != "RS256" {
			continue
		}
		pub, err := rsaPublicKeyFromJWK(k.N, k.E)
		if err == nil {
			keys[k.Kid] = pub
		}
	}
	if len(keys) == 0 {
		return errors.New("Google certs contained no usable RSA keys")
	}
	g.mu.Lock()
	g.keys = keys
	g.keysUntil = time.Now().Add(time.Hour)
	g.mu.Unlock()
	return nil
}

func rsaPublicKeyFromJWK(nValue, eValue string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nValue)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eValue)
	if err != nil {
		return nil, err
	}
	e := new(big.Int).SetBytes(eBytes)
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(e.Int64())}, nil
}

func parseJWTAudience(raw json.RawMessage) ([]string, error) {
	var single string
	if err := json.Unmarshal(raw, &single); err == nil {
		return []string{single}, nil
	}
	var many []string
	if err := json.Unmarshal(raw, &many); err == nil {
		return many, nil
	}
	return nil, errors.New("invalid JWT audience")
}

type userSessionClaims struct {
	Issuer     string           `json:"iss"`
	UserID     string           `json:"sub"`
	TenantID   uuid.UUID        `json:"tenant_id"`
	Role       permissions.Role `json:"role"`
	Name       string           `json:"name,omitempty"`
	Email      string           `json:"email,omitempty"`
	Avatar     string           `json:"avatar,omitempty"`
	IssuedAt   time.Time        `json:"iat"`
	ExpiresAt  time.Time        `json:"exp"`
	Provider   string           `json:"provider,omitempty"`
	ProviderID string           `json:"provider_id,omitempty"`
}

type userSessionSigner struct {
	secret []byte
}

func newUserSessionSigner(secret string) *userSessionSigner {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return nil
	}
	return &userSessionSigner{secret: []byte(secret)}
}

func (s *userSessionSigner) enabled() bool {
	return s != nil && len(s.secret) > 0
}

func (s *userSessionSigner) Sign(claims userSessionClaims) (string, error) {
	if !s.enabled() {
		return "", errors.New("session signer disabled")
	}
	if claims.Issuer == "" {
		claims.Issuer = "goclaw"
	}
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	unsigned := jwtEncode(headerJSON) + "." + jwtEncode(payloadJSON)
	return unsigned + "." + jwtEncode(s.mac([]byte(unsigned))), nil
}

func (s *userSessionSigner) Verify(token string) (*userSessionClaims, error) {
	if !s.enabled() {
		return nil, errors.New("session signer disabled")
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid session token")
	}
	unsigned := parts[0] + "." + parts[1]
	got, err := jwtDecode(parts[2])
	if err != nil {
		return nil, err
	}
	want := s.mac([]byte(unsigned))
	if !hmac.Equal(got, want) {
		return nil, errors.New("invalid session token signature")
	}
	payload, err := jwtDecode(parts[1])
	if err != nil {
		return nil, err
	}
	var claims userSessionClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	if claims.Issuer != "goclaw" || claims.UserID == "" || claims.ExpiresAt.Before(time.Now()) {
		return nil, errors.New("invalid session token claims")
	}
	if claims.Role == "" {
		claims.Role = permissions.RoleOperator
	}
	return &claims, nil
}

func (s *userSessionSigner) mac(data []byte) []byte {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write(data)
	return mac.Sum(nil)
}

func verifyUserSessionBearer(bearer string) (*userSessionClaims, bool) {
	if pkgUserSessionSigner == nil || strings.TrimSpace(bearer) == "" {
		return nil, false
	}
	claims, err := pkgUserSessionSigner.Verify(bearer)
	if err != nil {
		return nil, false
	}
	return claims, true
}

func googleAuthConfigFromEnv() googleAuthConfig {
	return googleAuthConfig{
		UserIDPrefix:   envFirst("GOCLAW_GOOGLE_AUTH_USER_ID_PREFIX", "GOCLAW_GOOGLE_USER_ID_PREFIX"),
		DefaultTenant:  envFirst("GOCLAW_GOOGLE_AUTH_TENANT", "GOCLAW_GOOGLE_AUTH_DEFAULT_TENANT"),
		DefaultRole:    envFirst("GOCLAW_GOOGLE_AUTH_ROLE", "GOCLAW_GOOGLE_AUTH_DEFAULT_ROLE"),
		SessionTTL:     parseDurationEnv("GOCLAW_GOOGLE_AUTH_SESSION_TTL", defaultGoogleSessionTTL),
		AllowedDomains: lowerStrings(parseCSVEnv("GOCLAW_GOOGLE_AUTH_ALLOWED_DOMAINS")),
	}
}

func googleAuthAudiences() []string {
	return append(parseCSVEnv("GOCLAW_GOOGLE_CLIENT_ID"), parseCSVEnv("GOCLAW_GOOGLE_CLIENT_IDS")...)
}

func GoogleAuthSessionSecretFromEnv(gatewayToken string) string {
	return googleFirstNonEmpty(
		os.Getenv("GOCLAW_GOOGLE_AUTH_SESSION_SECRET"),
		os.Getenv("GOCLAW_AUTH_SESSION_SECRET"),
		os.Getenv("GOCLAW_ENCRYPTION_KEY"),
		gatewayToken,
	)
}

func permissionRoleFromTenantRole(role string) permissions.Role {
	switch role {
	case store.TenantRoleOwner, store.TenantRoleAdmin:
		return permissions.RoleAdmin
	case store.TenantRoleViewer:
		return permissions.RoleViewer
	default:
		return permissions.RoleOperator
	}
}

func parseDurationEnv(key string, fallback time.Duration) time.Duration {
	if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil && d > 0 {
			return d
		}
	}
	return fallback
}

func parseCSVEnv(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func lowerStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func envFirst(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func googleFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func jwtEncode(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

func jwtDecode(value string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(value)
}

// randomSessionSecret is used only by tests that need an isolated signer.
func randomSessionSecret() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return jwtEncode(b[:])
}
