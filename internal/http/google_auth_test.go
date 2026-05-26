package http

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
	"github.com/nextlevelbuilder/goclaw/internal/permissions"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func TestGoogleAuthLoginCreatesTenantUserAndSession(t *testing.T) {
	resetAuthGlobalsForTest(t)
	InitUserSessionAuth(randomSessionSecret())

	tenantStore := newFakeGoogleTenantStore()
	h := NewGoogleAuthHandler(tenantStore, bus.New())
	h.provider = fakeGoogleProvider{identity: &googleIdentity{
		ProviderID: "google-sub-1",
		Name:       "Mochi User",
		Email:      "mochi@example.com",
		Avatar:     "https://example.com/avatar.png",
	}}
	h.cfg = googleAuthConfig{UserIDPrefix: "google", DefaultRole: store.TenantRoleOperator}

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	reqBody := []byte(`{"credential":"id-token"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/google/login", bytes.NewReader(reqBody))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("login status = %d body=%s", rec.Code, rec.Body.String())
	}
	var login googleLoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &login); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	if login.AccessToken == "" || login.TokenType != "Bearer" {
		t.Fatalf("login token missing: %#v", login)
	}
	if login.User.ID != "google:google-sub-1" || login.User.Email != "mochi@example.com" {
		t.Fatalf("login user = %#v", login.User)
	}
	member := tenantStore.members["google:google-sub-1"]
	if member == nil || member.Role != store.TenantRoleOperator || member.DisplayName == nil || *member.DisplayName != "Mochi User" {
		t.Fatalf("tenant member = %#v", member)
	}

	meReq := httptest.NewRequest(http.MethodGet, "/v1/auth/me", nil)
	meReq.Header.Set("Authorization", "Bearer "+login.AccessToken)
	meReq.Header.Set("X-GoClaw-User-Id", "google:spoofed")
	meRec := httptest.NewRecorder()
	mux.ServeHTTP(meRec, meReq)

	if meRec.Code != http.StatusOK {
		t.Fatalf("me status = %d body=%s", meRec.Code, meRec.Body.String())
	}
	var me map[string]any
	if err := json.Unmarshal(meRec.Body.Bytes(), &me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	user := me["user"].(map[string]any)
	if user["id"] != "google:google-sub-1" {
		t.Fatalf("me user id = %#v", user["id"])
	}
}

func TestGoogleAuthLoginRejectsDisallowedDomain(t *testing.T) {
	resetAuthGlobalsForTest(t)
	InitUserSessionAuth(randomSessionSecret())

	tenantStore := newFakeGoogleTenantStore()
	h := NewGoogleAuthHandler(tenantStore, nil)
	h.provider = fakeGoogleProvider{identity: &googleIdentity{
		ProviderID: "google-sub-1",
		Name:       "Mochi User",
		Email:      "mochi@blocked.test",
	}}
	h.cfg = googleAuthConfig{AllowedDomains: []string{"example.com"}}

	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/google/login", strings.NewReader(`{"credential":"id-token"}`))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestUserSessionSignerRejectsTampering(t *testing.T) {
	signer := newUserSessionSigner(randomSessionSecret())
	token, err := signer.Sign(userSessionClaims{
		UserID:    "google:user-1",
		TenantID:  store.MasterTenantID,
		Role:      permissions.RoleOperator,
		IssuedAt:  time.Now(),
		ExpiresAt: time.Now().Add(defaultGoogleSessionTTL),
	})
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, err := signer.Verify(token); err != nil {
		t.Fatalf("verify: %v", err)
	}
	tampered := token[:len(token)-1] + "x"
	if _, err := signer.Verify(tampered); err == nil {
		t.Fatal("tampered token verified")
	}
}

type fakeGoogleProvider struct {
	identity *googleIdentity
	err      error
}

func (f fakeGoogleProvider) Authorize(context.Context, googleLoginRequest) (*googleIdentity, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.identity, nil
}

type fakeGoogleTenantStore struct {
	tenant  store.TenantData
	members map[string]*store.TenantUserData
}

func newFakeGoogleTenantStore() *fakeGoogleTenantStore {
	return &fakeGoogleTenantStore{
		tenant: store.TenantData{
			ID:     store.MasterTenantID,
			Name:   "Master",
			Slug:   "master",
			Status: store.TenantStatusActive,
		},
		members: map[string]*store.TenantUserData{},
	}
}

func (f *fakeGoogleTenantStore) CreateTenant(context.Context, *store.TenantData) error { return nil }
func (f *fakeGoogleTenantStore) GetTenant(_ context.Context, id uuid.UUID) (*store.TenantData, error) {
	if id == f.tenant.ID {
		return &f.tenant, nil
	}
	return nil, nil
}
func (f *fakeGoogleTenantStore) GetTenantBySlug(_ context.Context, slug string) (*store.TenantData, error) {
	if slug == f.tenant.Slug {
		return &f.tenant, nil
	}
	return nil, nil
}
func (f *fakeGoogleTenantStore) ListTenants(context.Context) ([]store.TenantData, error) {
	return []store.TenantData{f.tenant}, nil
}
func (f *fakeGoogleTenantStore) UpdateTenant(context.Context, uuid.UUID, map[string]any) error {
	return nil
}
func (f *fakeGoogleTenantStore) AddUser(context.Context, uuid.UUID, string, string) error { return nil }
func (f *fakeGoogleTenantStore) RemoveUser(context.Context, uuid.UUID, string) error      { return nil }
func (f *fakeGoogleTenantStore) GetUserRole(_ context.Context, _ uuid.UUID, userID string) (string, error) {
	if member := f.members[userID]; member != nil {
		return member.Role, nil
	}
	return "", nil
}
func (f *fakeGoogleTenantStore) ListUsers(context.Context, uuid.UUID) ([]store.TenantUserData, error) {
	return nil, nil
}
func (f *fakeGoogleTenantStore) ListUserTenants(_ context.Context, userID string) ([]store.TenantUserData, error) {
	if member := f.members[userID]; member != nil {
		return []store.TenantUserData{*member}, nil
	}
	return nil, nil
}
func (f *fakeGoogleTenantStore) GetTenantsByIDs(context.Context, []uuid.UUID) ([]store.TenantData, error) {
	return []store.TenantData{f.tenant}, nil
}
func (f *fakeGoogleTenantStore) ResolveUserTenant(context.Context, string) (uuid.UUID, error) {
	return f.tenant.ID, nil
}
func (f *fakeGoogleTenantStore) GetTenantUser(context.Context, uuid.UUID) (*store.TenantUserData, error) {
	return nil, nil
}
func (f *fakeGoogleTenantStore) CreateTenantUserReturning(_ context.Context, tenantID uuid.UUID, userID, displayName, role string) (*store.TenantUserData, error) {
	if member := f.members[userID]; member != nil {
		return member, nil
	}
	id := store.GenNewID()
	member := &store.TenantUserData{
		ID:          id,
		TenantID:    tenantID,
		UserID:      userID,
		DisplayName: &displayName,
		Role:        role,
	}
	f.members[userID] = member
	return member, nil
}

func resetAuthGlobalsForTest(t *testing.T) {
	t.Helper()
	oldGatewayToken := pkgGatewayToken
	oldAPIKeyCache := pkgAPIKeyCache
	oldPairingStore := pkgPairingStore
	oldTenantCache := pkgTenantCache
	oldOwnerIDs := pkgOwnerIDs
	oldSessionSigner := pkgUserSessionSigner
	pkgGatewayToken = "gateway-token"
	pkgAPIKeyCache = nil
	pkgPairingStore = nil
	pkgTenantCache = nil
	pkgOwnerIDs = nil
	pkgUserSessionSigner = nil
	t.Cleanup(func() {
		pkgGatewayToken = oldGatewayToken
		pkgAPIKeyCache = oldAPIKeyCache
		pkgPairingStore = oldPairingStore
		pkgTenantCache = oldTenantCache
		pkgOwnerIDs = oldOwnerIDs
		pkgUserSessionSigner = oldSessionSigner
	})
}
