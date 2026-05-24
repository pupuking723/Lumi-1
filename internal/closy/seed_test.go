package closy

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/nextlevelbuilder/goclaw/internal/bootstrap"
	"github.com/nextlevelbuilder/goclaw/internal/store"
)

func TestContextFilesIncludeMochiPromptSurface(t *testing.T) {
	files := ContextFiles()
	for _, name := range []string{
		bootstrap.AgentsFile,
		bootstrap.SoulFile,
		bootstrap.IdentityFile,
		bootstrap.CapabilitiesFile,
		bootstrap.UserPredefinedFile,
		ResponseStrategyFile,
		CoreScenariosFile,
	} {
		if files[name] == "" {
			t.Fatalf("expected %s to be present", name)
		}
	}
	if got := files[bootstrap.IdentityFile]; !containsAll(got, "Mochi", "fashion companion", "body-shame") {
		t.Fatalf("identity file does not describe Mochi: %s", got)
	}
	if got := files[ResponseStrategyFile]; !containsAll(got, "Strongly style-related", "Weakly style-related state", "Risk or out-of-lane") {
		t.Fatalf("response strategy file does not include routing rules: %s", got)
	}
	if got := files[CoreScenariosFile]; !containsAll(got, "Outfit / OOTD Review", "Shopping / Purchase Decision", "Light Emotional State") {
		t.Fatalf("core scenarios file does not include Phase 1 playbook: %s", got)
	}
}

func TestCurrentSeedManifestIncludesVersionAndChecksums(t *testing.T) {
	manifest := CurrentSeedManifest()
	if manifest.Version != SeedVersion {
		t.Fatalf("manifest version = %q, want %q", manifest.Version, SeedVersion)
	}
	if manifest.AgentKey != AgentKey || manifest.DisplayName != DisplayName {
		t.Fatalf("manifest identity = %+v", manifest)
	}
	if !strings.HasPrefix(manifest.Checksum, "sha256:") {
		t.Fatalf("manifest checksum = %q", manifest.Checksum)
	}
	for name, content := range ContextFiles() {
		if manifest.ContextFiles[name] != checksum(content) {
			t.Fatalf("%s checksum = %q, want %q", name, manifest.ContextFiles[name], checksum(content))
		}
	}
}

func TestEnsureSeedCreatesMochiWithManifestAndContextFiles(t *testing.T) {
	agents := newSeedAgentStore()

	agent, created, err := EnsureSeed(context.Background(), agents, SeedOptions{
		WorkspaceRoot: "/tmp/goclaw-workspace",
		Provider:      "shortapi",
		Model:         "openai/gpt-5.4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected seed to create agent")
	}
	if agent.AgentKey != AgentKey || agent.DisplayName != DisplayName {
		t.Fatalf("agent identity = %s/%s", agent.AgentKey, agent.DisplayName)
	}
	manifest := seedManifestFromOtherConfig(agent.OtherConfig)
	if manifest == nil || manifest.Version != SeedVersion {
		t.Fatalf("expected seed manifest in other_config, got %s", string(agent.OtherConfig))
	}
	for name, content := range ContextFiles() {
		if got := agents.contextFiles[name]; got != content {
			t.Fatalf("%s content mismatch", name)
		}
	}
}

func TestEnsureSeedIsNoopOnRepeat(t *testing.T) {
	agents := newSeedAgentStore()
	if _, _, err := EnsureSeed(context.Background(), agents, SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	agents.resetCounters()

	_, created, err := EnsureSeed(context.Background(), agents, SeedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("repeat seed should not create a new agent")
	}
	if len(agents.setCalls) != 0 {
		t.Fatalf("repeat seed wrote context files: %v", agents.setCalls)
	}
	if agents.updateCalls != 0 {
		t.Fatalf("repeat seed updated agent %d times, want 0", agents.updateCalls)
	}
}

func TestEnsureSeedBackfillsMissingContextFile(t *testing.T) {
	agents := newSeedAgentStore()
	if _, _, err := EnsureSeed(context.Background(), agents, SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	delete(agents.contextFiles, ResponseStrategyFile)
	agents.resetCounters()

	_, _, err := EnsureSeed(context.Background(), agents, SeedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := agents.contextFiles[ResponseStrategyFile]; got != ContextFiles()[ResponseStrategyFile] {
		t.Fatalf("response strategy file was not backfilled")
	}
	if len(agents.setCalls) != 1 || agents.setCalls[0] != ResponseStrategyFile {
		t.Fatalf("set calls = %v, want [%s]", agents.setCalls, ResponseStrategyFile)
	}
}

func TestEnsureSeedPreservesModifiedIdentityAndSoul(t *testing.T) {
	agents := newSeedAgentStore()
	if _, _, err := EnsureSeed(context.Background(), agents, SeedOptions{}); err != nil {
		t.Fatal(err)
	}
	customIdentity := "# identity.md\n\nLocal Mochi edits."
	customSoul := "# soul.md\n\nLocal Mochi voice edits."
	agents.contextFiles[bootstrap.IdentityFile] = customIdentity
	agents.contextFiles[bootstrap.SoulFile] = customSoul
	agents.resetCounters()

	_, _, err := EnsureSeed(context.Background(), agents, SeedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if agents.contextFiles[bootstrap.IdentityFile] != customIdentity {
		t.Fatal("identity file was overwritten")
	}
	if agents.contextFiles[bootstrap.SoulFile] != customSoul {
		t.Fatal("soul file was overwritten")
	}
	if len(agents.setCalls) != 0 {
		t.Fatalf("modified files should not be rewritten, got %v", agents.setCalls)
	}
}

func TestEnsureSeedMigratesPristinePreviousVersion(t *testing.T) {
	const oldSoul = "# soul.md\n\nOld seed soul."
	agents := seededExistingAgentWithPreviousManifest(map[string]string{
		bootstrap.SoulFile: oldSoul,
	})
	agents.resetCounters()

	_, _, err := EnsureSeed(context.Background(), agents, SeedOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := agents.contextFiles[bootstrap.SoulFile]; got != ContextFiles()[bootstrap.SoulFile] {
		t.Fatal("pristine previous seed file was not migrated")
	}
	if len(agents.setCalls) != 1 || agents.setCalls[0] != bootstrap.SoulFile {
		t.Fatalf("set calls = %v, want [%s]", agents.setCalls, bootstrap.SoulFile)
	}
	if agents.updateCalls != 1 {
		t.Fatalf("manifest update calls = %d, want 1", agents.updateCalls)
	}
	manifest := seedManifestFromOtherConfig(agents.agent.OtherConfig)
	if manifest == nil || manifest.Version != SeedVersion {
		t.Fatalf("manifest was not updated: %s", string(agents.agent.OtherConfig))
	}
}

func TestEnsureSeedCanSkipSeedMigration(t *testing.T) {
	const oldSoul = "# soul.md\n\nOld seed soul."
	agents := seededExistingAgentWithPreviousManifest(map[string]string{
		bootstrap.SoulFile: oldSoul,
	})
	agents.resetCounters()

	_, _, err := EnsureSeed(context.Background(), agents, SeedOptions{SkipSeedMigrations: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := agents.contextFiles[bootstrap.SoulFile]; got != oldSoul {
		t.Fatal("seed migration should have been skipped")
	}
	if len(agents.setCalls) != 0 {
		t.Fatalf("skipped seed migration wrote files: %v", agents.setCalls)
	}
	if agents.updateCalls != 0 {
		t.Fatalf("skipped seed migration should keep old manifest, update calls = %d", agents.updateCalls)
	}
}

func TestPhase1PromptRegressionCasesCoverRoutingAndSafety(t *testing.T) {
	cases := Phase1PromptRegressionCases()
	if len(cases) < 10 {
		t.Fatalf("case count = %d, want at least 10", len(cases))
	}
	seen := map[string]bool{}
	for _, tc := range cases {
		if tc.Name == "" || tc.Input == "" || tc.ExpectedBehavior == "" {
			t.Fatalf("incomplete prompt regression case: %+v", tc)
		}
		seen[tc.Name] = true
	}
	for _, name := range []string{"strong_style", "weak_state", "unrelated", "therapy_boundary", "medical_boundary", "body_shame_reframe"} {
		if !seen[name] {
			t.Fatalf("missing prompt regression case %q", name)
		}
	}
}

func TestMediaToolSettingsUseSelectedProvider(t *testing.T) {
	settings, err := mediaToolSettings("zai-coding", "glm-5.1")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"read_image", "read_audio"} {
		var parsed struct {
			Providers []struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
				Enabled  bool   `json:"enabled"`
			} `json:"providers"`
		}
		if err := json.Unmarshal(settings[name], &parsed); err != nil {
			t.Fatalf("%s settings should be valid json: %v", name, err)
		}
		if len(parsed.Providers) != 1 {
			t.Fatalf("%s provider count = %d, want 1", name, len(parsed.Providers))
		}
		provider := parsed.Providers[0]
		if provider.Provider != "zai-coding" || provider.Model != "glm-5.1" || !provider.Enabled {
			t.Fatalf("%s provider = %+v", name, provider)
		}
	}
}

func containsAll(s string, needles ...string) bool {
	for _, needle := range needles {
		if !contains(s, needle) {
			return false
		}
	}
	return true
}

func contains(s, needle string) bool {
	return strings.Contains(s, needle)
}

func seededExistingAgentWithPreviousManifest(overrides map[string]string) *seedAgentStore {
	files := ContextFiles()
	contextFiles := make(map[string]string, len(files))
	previousManifest := SeedManifest{
		Product:      "closy",
		AgentKey:     AgentKey,
		DisplayName:  DisplayName,
		Version:      "2026-05-21.1",
		ContextFiles: make(map[string]string, len(files)),
	}
	for name, content := range files {
		if override, ok := overrides[name]; ok {
			content = override
		}
		contextFiles[name] = content
		previousManifest.ContextFiles[name] = checksum(content)
	}
	previousManifest.Checksum = "sha256:previous"
	rawManifest, _ := json.Marshal(previousManifest)
	otherConfig := json.RawMessage(fmt.Sprintf(`{"product":"closy","prompt_mode":"full","seed":%s}`, rawManifest))
	return &seedAgentStore{
		agent: &store.AgentData{
			BaseModel:   store.BaseModel{ID: uuid.New()},
			AgentKey:    AgentKey,
			DisplayName: DisplayName,
			AgentType:   store.AgentTypePredefined,
			OtherConfig: otherConfig,
		},
		contextFiles: contextFiles,
	}
}

type seedAgentStore struct {
	agent        *store.AgentData
	contextFiles map[string]string
	setCalls     []string
	updateCalls  int
}

func newSeedAgentStore() *seedAgentStore {
	return &seedAgentStore{contextFiles: map[string]string{}}
}

func (s *seedAgentStore) resetCounters() {
	s.setCalls = nil
	s.updateCalls = 0
}

func (s *seedAgentStore) Create(_ context.Context, agent *store.AgentData) error {
	copied := *agent
	if copied.ID == uuid.Nil {
		copied.ID = uuid.New()
	}
	s.agent = &copied
	agent.ID = copied.ID
	return nil
}

func (s *seedAgentStore) GetByKey(_ context.Context, agentKey string) (*store.AgentData, error) {
	if s.agent == nil || s.agent.AgentKey != agentKey {
		return nil, fmt.Errorf("agent not found: %s", agentKey)
	}
	copied := *s.agent
	return &copied, nil
}

func (s *seedAgentStore) GetByID(_ context.Context, id uuid.UUID) (*store.AgentData, error) {
	if s.agent == nil || s.agent.ID != id {
		return nil, fmt.Errorf("agent not found: %s", id)
	}
	copied := *s.agent
	return &copied, nil
}

func (s *seedAgentStore) GetByIDUnscoped(ctx context.Context, id uuid.UUID) (*store.AgentData, error) {
	return s.GetByID(ctx, id)
}

func (s *seedAgentStore) GetByKeys(_ context.Context, keys []string) ([]store.AgentData, error) {
	var out []store.AgentData
	for _, key := range keys {
		if s.agent != nil && s.agent.AgentKey == key {
			out = append(out, *s.agent)
		}
	}
	return out, nil
}

func (s *seedAgentStore) GetByIDs(_ context.Context, ids []uuid.UUID) ([]store.AgentData, error) {
	var out []store.AgentData
	for _, id := range ids {
		if s.agent != nil && s.agent.ID == id {
			out = append(out, *s.agent)
		}
	}
	return out, nil
}

func (s *seedAgentStore) Update(_ context.Context, id uuid.UUID, updates map[string]any) error {
	if s.agent == nil || s.agent.ID != id {
		return fmt.Errorf("agent not found: %s", id)
	}
	if raw, ok := updates["other_config"]; ok {
		switch v := raw.(type) {
		case json.RawMessage:
			s.agent.OtherConfig = v
		case []byte:
			s.agent.OtherConfig = json.RawMessage(v)
		case string:
			s.agent.OtherConfig = json.RawMessage(v)
		default:
			return fmt.Errorf("unsupported other_config type %T", raw)
		}
	}
	s.updateCalls++
	return nil
}

func (s *seedAgentStore) Delete(_ context.Context, _ uuid.UUID) error { return nil }
func (s *seedAgentStore) List(_ context.Context, _ string) ([]store.AgentData, error) {
	if s.agent == nil {
		return nil, nil
	}
	return []store.AgentData{*s.agent}, nil
}
func (s *seedAgentStore) GetDefault(_ context.Context) (*store.AgentData, error) {
	if s.agent == nil {
		return nil, fmt.Errorf("agent not found: default")
	}
	copied := *s.agent
	return &copied, nil
}
func (s *seedAgentStore) ResetStuckSummoning(_ context.Context) (int64, error) { return 0, nil }
func (s *seedAgentStore) ShareAgent(_ context.Context, _ uuid.UUID, _, _, _ string) error {
	return nil
}
func (s *seedAgentStore) RevokeShare(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}
func (s *seedAgentStore) ListShares(_ context.Context, _ uuid.UUID) ([]store.AgentShareData, error) {
	return nil, nil
}
func (s *seedAgentStore) CanAccess(_ context.Context, _ uuid.UUID, _ string) (bool, string, error) {
	return true, "owner", nil
}
func (s *seedAgentStore) ListAccessible(_ context.Context, _ string) ([]store.AgentData, error) {
	return s.List(context.Background(), "")
}
func (s *seedAgentStore) GetAgentContextFiles(_ context.Context, agentID uuid.UUID) ([]store.AgentContextFileData, error) {
	names := make([]string, 0, len(s.contextFiles))
	for name := range s.contextFiles {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]store.AgentContextFileData, 0, len(names))
	for _, name := range names {
		out = append(out, store.AgentContextFileData{AgentID: agentID, FileName: name, Content: s.contextFiles[name]})
	}
	return out, nil
}
func (s *seedAgentStore) SetAgentContextFile(_ context.Context, _ uuid.UUID, fileName, content string) error {
	if s.contextFiles == nil {
		s.contextFiles = map[string]string{}
	}
	s.contextFiles[fileName] = content
	s.setCalls = append(s.setCalls, fileName)
	return nil
}
func (s *seedAgentStore) PropagateContextFile(_ context.Context, _ uuid.UUID, _ string) (int, error) {
	return 0, nil
}
func (s *seedAgentStore) GetUserContextFiles(_ context.Context, _ uuid.UUID, _ string) ([]store.UserContextFileData, error) {
	return nil, nil
}
func (s *seedAgentStore) ListUserContextFilesByName(_ context.Context, _ uuid.UUID, _ string) ([]store.UserContextFileData, error) {
	return nil, nil
}
func (s *seedAgentStore) SetUserContextFile(_ context.Context, _ uuid.UUID, _, _, _ string) error {
	return nil
}
func (s *seedAgentStore) DeleteUserContextFile(_ context.Context, _ uuid.UUID, _, _ string) error {
	return nil
}
func (s *seedAgentStore) MigrateUserDataOnMerge(_ context.Context, _ []string, _ string) error {
	return nil
}
func (s *seedAgentStore) GetUserOverride(_ context.Context, _ uuid.UUID, _ string) (*store.UserAgentOverrideData, error) {
	return nil, nil
}
func (s *seedAgentStore) SetUserOverride(_ context.Context, _ *store.UserAgentOverrideData) error {
	return nil
}
func (s *seedAgentStore) GetOrCreateUserProfile(_ context.Context, _ uuid.UUID, _, _, _ string) (bool, string, error) {
	return false, "", nil
}
func (s *seedAgentStore) EnsureUserProfile(_ context.Context, _ uuid.UUID, _ string) error {
	return nil
}
func (s *seedAgentStore) ListUserInstances(_ context.Context, _ uuid.UUID) ([]store.UserInstanceData, error) {
	return nil, nil
}
func (s *seedAgentStore) UpdateUserProfileMetadata(_ context.Context, _ uuid.UUID, _ string, _ map[string]string) error {
	return nil
}
