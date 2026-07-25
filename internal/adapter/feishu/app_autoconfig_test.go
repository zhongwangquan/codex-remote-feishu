package feishu

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
	"time"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkapplication "github.com/larksuite/oapi-sdk-go/v3/service/application/v6"

	"github.com/kxn/codex-remote-feishu/internal/feishuapp"
)

func TestGetApplicationConfigIncludesLanguageQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case larkcore.TenantAccessTokenInternalUrlPath:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":                0,
				"msg":                 "ok",
				"tenant_access_token": "tenant-token",
				"expire":              7200,
			})
		case "/open-apis/application/v6/applications/cli_xxx":
			if got := r.URL.Query().Get("lang"); got != "zh_cn" {
				t.Fatalf("lang = %q, want zh_cn", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "success",
				"data": map[string]any{
					"app": map[string]any{"app_id": "cli_xxx"},
				},
			})
		default:
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := lark.NewClient(
		"cli_xxx",
		"secret_xxx",
		lark.WithOpenBaseUrl(server.URL),
		lark.WithHttpClient(server.Client()),
		lark.WithReqTimeout(5*time.Second),
	)
	app, err := getApplicationConfig(
		context.Background(),
		NewFeishuCallBroker("main", client),
		client,
		"cli_xxx",
	)
	if err != nil {
		t.Fatalf("getApplicationConfig: %v", err)
	}
	if app == nil || app.AppId == nil || *app.AppId != "cli_xxx" {
		t.Fatalf("unexpected app response: %#v", app)
	}
}

func TestGetApplicationVersionIncludesLanguageQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case larkcore.TenantAccessTokenInternalUrlPath:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":                0,
				"msg":                 "ok",
				"tenant_access_token": "tenant-token",
				"expire":              7200,
			})
		case "/open-apis/application/v6/applications/cli_xxx/app_versions/version-1":
			if got := r.URL.Query().Get("lang"); got != "zh_cn" {
				t.Fatalf("lang = %q, want zh_cn", got)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"msg":  "success",
				"data": map[string]any{
					"app_version": map[string]any{"version_id": "version-1"},
				},
			})
		default:
			t.Fatalf("unexpected request path: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := lark.NewClient(
		"cli_xxx",
		"secret_xxx",
		lark.WithOpenBaseUrl(server.URL),
		lark.WithHttpClient(server.Client()),
		lark.WithReqTimeout(5*time.Second),
	)
	version, err := getApplicationVersion(
		context.Background(),
		NewFeishuCallBroker("main", client),
		client,
		"cli_xxx",
		"version-1",
	)
	if err != nil {
		t.Fatalf("getApplicationVersion: %v", err)
	}
	if version == nil || version.VersionId == nil || *version.VersionId != "version-1" {
		t.Fatalf("unexpected version response: %#v", version)
	}
}

func TestBuildConfigPatchRequestPreservesExistingConfigAndOmitsBlankWebsocketURLs(t *testing.T) {
	service := newAutoConfigService(nil, testAutoConfigManifest(), feishuapp.DefaultFixedPolicy())
	req := service.buildConfigPatchRequest(AutoConfigDiff{
		MissingScopes: []AutoConfigScopeRef{
			{Scope: "im:message", ScopeType: "tenant"},
		},
		ExtraScopes: []AutoConfigScopeRef{
			{Scope: "application:application:patch", ScopeType: "tenant"},
		},
		MissingEvents:                 []string{"im.message.receive_v1"},
		ExtraEvents:                   []string{"legacy.event"},
		EventSubscriptionTypeMismatch: true,
		MissingCallbacks:              []string{"card.action.trigger"},
		ExtraCallbacks:                []string{"legacy.callback"},
		CallbackTypeMismatch:          true,
	})

	if req.Scope == nil || !reflect.DeepEqual(req.Scope.AddScopes, []v7PatchConfigScopeItem{{
		ScopeName: "im:message",
		TokenType: "tenant",
	}}) {
		t.Fatalf("unexpected scope additions: %#v", req.Scope)
	}
	if len(req.Scope.RemoveScopes) != 0 {
		t.Fatalf("existing scopes must be preserved, got removals %#v", req.Scope.RemoveScopes)
	}
	if req.Event == nil || req.Event.RequestURL != nil {
		t.Fatalf("blank websocket event URL must be omitted, got %#v", req.Event)
	}
	if len(req.Event.RemoveEvents) != 0 {
		t.Fatalf("existing events must be preserved, got removals %#v", req.Event.RemoveEvents)
	}
	if req.Callback == nil || req.Callback.RequestURL != nil {
		t.Fatalf("blank websocket callback URL must be omitted, got %#v", req.Callback)
	}
	if len(req.Callback.RemoveCallbacks) != 0 {
		t.Fatalf("existing callbacks must be preserved, got removals %#v", req.Callback.RemoveCallbacks)
	}
}

func TestBuildPlanReportsExtrasWithoutRequiringDestructivePatch(t *testing.T) {
	manifest := testAutoConfigManifest()
	service := newAutoConfigService(nil, manifest, feishuapp.DefaultFixedPolicy())
	version := &larkapplication.ApplicationAppVersion{
		VersionId: strp("online-1"),
		Version:   strp("1.0.0"),
		Status:    intp(larkapplication.AppVersionStatusAudited),
		Ability: &larkapplication.AppAbility{
			Bot: &larkapplication.Bot{},
		},
	}
	plan := service.buildPlan(autoConfigSnapshot{
		app: &larkapplication.Application{
			Scopes: []*larkapplication.AppScope{
				{Scope: strp("im:message"), TokenTypes: []string{"tenant"}},
				{Scope: strp("drive:drive"), TokenTypes: []string{"tenant"}},
				{Scope: strp("application:application:patch"), TokenTypes: []string{"tenant"}},
			},
			Event: &larkapplication.SubscribedEvent{
				SubscriptionType: strp("websocket"),
				RequestUrl:       strp(""),
				SubscribedEvents: []string{"im.message.receive_v1", "legacy.event"},
			},
			Callback: &larkapplication.Callback{
				CallbackType:        strp("websocket"),
				RequestUrl:          strp(""),
				SubscribedCallbacks: []string{"card.action.trigger", "legacy.callback"},
			},
			OnlineVersionId: strp("online-1"),
		},
		onlineVersion: version,
		activeVersion: version,
	})

	if plan.Diff.ConfigPatchRequired {
		t.Fatalf("extra existing config must not require a destructive patch: %#v", plan.Diff)
	}
	if len(plan.Diff.ExtraScopes) != 1 || len(plan.Diff.ExtraEvents) != 1 || len(plan.Diff.ExtraCallbacks) != 1 {
		t.Fatalf("extras should remain observable: %#v", plan.Diff)
	}
}

func TestActiveVersionEventsPrefersStableEventIdentifiers(t *testing.T) {
	version := &larkapplication.ApplicationAppVersion{
		Events: []string{"接收消息"},
		EventInfos: []*larkapplication.Event{
			{
				EventType: strp("im.message.receive_v1"),
				EventName: strp("接收消息"),
			},
		},
	}

	if got := activeVersionEvents(version); !reflect.DeepEqual(got, []string{"im.message.receive_v1"}) {
		t.Fatalf("active version events = %#v, want stable event identifiers", got)
	}
}

func TestBuildPlanTrustsManagedPublishedVersionWhenApplicationReadbackOmitsConfig(t *testing.T) {
	manifest := testAutoConfigManifest()
	service := newAutoConfigService(nil, manifest, feishuapp.DefaultFixedPolicy())
	version := &larkapplication.ApplicationAppVersion{
		VersionId: strp("online-1"),
		Version:   strp("1.0.1"),
		Status:    intp(larkapplication.AppVersionStatusAudited),
		Ability: &larkapplication.AppAbility{
			Bot: &larkapplication.Bot{},
		},
		Remark: &larkapplication.AppVersionRemark{
			Remark:       strp(autoConfigDefaultPublishRemark),
			UpdateRemark: strp(autoConfigDefaultPublishChangelog),
		},
		Events: []string{"接收消息"},
		EventInfos: []*larkapplication.Event{
			{
				EventType: strp("im.message.receive_v1"),
				EventName: strp("接收消息"),
			},
		},
	}
	plan := service.buildPlan(autoConfigSnapshot{
		app: &larkapplication.Application{
			Scopes: []*larkapplication.AppScope{
				{Scope: strp("im:message"), TokenTypes: []string{"tenant"}},
				{Scope: strp("drive:drive"), TokenTypes: []string{"tenant"}},
			},
			OnlineVersionId: strp("online-1"),
		},
		grantedScopes: []AppScopeStatus{
			{ScopeName: "im:message", ScopeType: "tenant", GrantStatus: 1},
			{ScopeName: "drive:drive", ScopeType: "tenant", GrantStatus: 1},
		},
		onlineVersion: version,
		activeVersion: version,
	})

	if plan.Status != AutoConfigStatusClean {
		t.Fatalf("plan status = %q, want %q: %#v", plan.Status, AutoConfigStatusClean, plan.Diff)
	}
	if plan.Diff.ConfigPatchRequired || plan.Diff.AbilityPatchRequired || plan.Diff.PublishRequired {
		t.Fatalf("managed published version must be converged despite weak app readback: %#v", plan.Diff)
	}
	if !reflect.DeepEqual(plan.Current.ConfiguredEvents, []string{"im.message.receive_v1"}) {
		t.Fatalf("configured events = %#v", plan.Current.ConfiguredEvents)
	}
	if !reflect.DeepEqual(plan.Current.ConfiguredCallbacks, []string{"card.action.trigger"}) {
		t.Fatalf("configured callbacks = %#v", plan.Current.ConfiguredCallbacks)
	}
}

func TestPlanAppAutoConfigReportsDiffAndRequirementState(t *testing.T) {
	restoreAutoConfigHooks(t)
	autoConfigListScopes = func(*SetupClient, context.Context) ([]AppScopeStatus, error) {
		return []AppScopeStatus{{ScopeName: "im:message", ScopeType: "tenant", GrantStatus: 1}}, nil
	}
	autoConfigGetApplication = func(context.Context, *FeishuCallBroker, *lark.Client, string) (*larkapplication.Application, error) {
		return &larkapplication.Application{
			Scopes: []*larkapplication.AppScope{
				{Scope: strp("im:message"), TokenTypes: []string{"tenant"}},
			},
			Event: &larkapplication.SubscribedEvent{
				SubscriptionType: strp("webhook"),
				RequestUrl:       strp("https://legacy.example.com"),
			},
			Callback: &larkapplication.Callback{
				CallbackType: strp("websocket"),
			},
			OnlineVersionId: strp("online-1"),
		}, nil
	}
	autoConfigGetApplicationVersion = func(context.Context, *FeishuCallBroker, *lark.Client, string, string) (*larkapplication.ApplicationAppVersion, error) {
		return &larkapplication.ApplicationAppVersion{
			VersionId: strp("online-1"),
			Version:   strp("1.0.0"),
			Status:    intp(larkapplication.AppVersionStatusAudited),
		}, nil
	}

	plan, err := PlanAppAutoConfig(context.Background(), LiveGatewayConfig{GatewayID: "main", AppID: "cli_xxx"}, testAutoConfigManifest(), feishuapp.DefaultFixedPolicy())
	if err != nil {
		t.Fatalf("PlanAppAutoConfig: %v", err)
	}
	if plan.Status != AutoConfigStatusApplyRequired {
		t.Fatalf("plan status = %q, want %q", plan.Status, AutoConfigStatusApplyRequired)
	}
	if !plan.Diff.ConfigPatchRequired || !plan.Diff.AbilityPatchRequired {
		t.Fatalf("expected config+ability patch required, got %#v", plan.Diff)
	}
	if !plan.Diff.EventSubscriptionTypeMismatch || !plan.Diff.EventRequestURLMismatch {
		t.Fatalf("expected event policy mismatch, got %#v", plan.Diff)
	}
	if !reflect.DeepEqual(plan.Diff.MissingEvents, []string{"im.message.receive_v1"}) {
		t.Fatalf("missing events = %#v", plan.Diff.MissingEvents)
	}
	if !reflect.DeepEqual(plan.Diff.MissingCallbacks, []string{"card.action.trigger"}) {
		t.Fatalf("missing callbacks = %#v", plan.Diff.MissingCallbacks)
	}
	if len(plan.BlockingRequirements) != 2 {
		t.Fatalf("blocking requirements = %#v", plan.BlockingRequirements)
	}
	if len(plan.DegradableRequirements) != 1 || plan.DegradableRequirements[0].Key != "drive:drive" {
		t.Fatalf("degradable requirements = %#v", plan.DegradableRequirements)
	}
}

func TestApplyAppAutoConfigEnablesBotBeforeConfigPatch(t *testing.T) {
	restoreAutoConfigHooks(t)
	phase := 0
	var calls []string
	autoConfigListScopes = func(*SetupClient, context.Context) ([]AppScopeStatus, error) {
		return nil, nil
	}
	autoConfigGetApplication = func(context.Context, *FeishuCallBroker, *lark.Client, string) (*larkapplication.Application, error) {
		if phase == 0 {
			return &larkapplication.Application{}, nil
		}
		return &larkapplication.Application{
			Scopes: []*larkapplication.AppScope{
				{Scope: strp("im:message"), TokenTypes: []string{"tenant"}},
				{Scope: strp("drive:drive"), TokenTypes: []string{"tenant"}},
			},
			Event: &larkapplication.SubscribedEvent{
				SubscriptionType: strp("websocket"),
				RequestUrl:       strp(""),
				SubscribedEvents: []string{"im.message.receive_v1"},
			},
			Callback: &larkapplication.Callback{
				CallbackType:        strp("websocket"),
				RequestUrl:          strp(""),
				SubscribedCallbacks: []string{"card.action.trigger"},
			},
			UnauditVersionId: strp("draft-1"),
		}, nil
	}
	autoConfigGetApplicationVersion = func(context.Context, *FeishuCallBroker, *lark.Client, string, string) (*larkapplication.ApplicationAppVersion, error) {
		if phase == 0 {
			return nil, nil
		}
		return &larkapplication.ApplicationAppVersion{
			VersionId: strp("draft-1"),
			Version:   strp("1.0.1"),
			Status:    intp(larkapplication.AppVersionStatusUnaudit),
			Ability: &larkapplication.AppAbility{
				Bot: &larkapplication.Bot{},
			},
		}, nil
	}
	autoConfigPatchAbility = func(context.Context, *FeishuCallBroker, *lark.Client, string, v7PatchAbilityRequest) error {
		calls = append(calls, "ability")
		return nil
	}
	autoConfigPatchConfig = func(context.Context, *FeishuCallBroker, *lark.Client, string, v7PatchConfigRequest) error {
		calls = append(calls, "config")
		phase = 1
		return nil
	}

	result, err := ApplyAppAutoConfig(context.Background(), LiveGatewayConfig{GatewayID: "main", AppID: "cli_xxx"}, testAutoConfigManifest(), feishuapp.DefaultFixedPolicy())
	if err != nil {
		t.Fatalf("ApplyAppAutoConfig: %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"ability", "config"}) {
		t.Fatalf("patch order = %#v, want ability then config", calls)
	}
	if result.Status != AutoConfigStatusPublishRequired {
		t.Fatalf("apply status = %q, want %q", result.Status, AutoConfigStatusPublishRequired)
	}
	if !result.Plan.Publish.NeedsPublish {
		t.Fatalf("expected publish to be required after apply, got %#v", result.Plan.Publish)
	}
}

func TestApplyAppAutoConfigAdvancesToPublishWhenReadbackLags(t *testing.T) {
	restoreAutoConfigHooks(t)
	autoConfigListScopes = func(*SetupClient, context.Context) ([]AppScopeStatus, error) {
		return []AppScopeStatus{
			{ScopeName: "im:message", ScopeType: "tenant", GrantStatus: 1},
			{ScopeName: "drive:drive", ScopeType: "tenant", GrantStatus: 1},
		}, nil
	}
	autoConfigGetApplication = func(context.Context, *FeishuCallBroker, *lark.Client, string) (*larkapplication.Application, error) {
		return &larkapplication.Application{
			Scopes: []*larkapplication.AppScope{
				{Scope: strp("im:message"), TokenTypes: []string{"tenant"}},
				{Scope: strp("drive:drive"), TokenTypes: []string{"tenant"}},
			},
			Event: &larkapplication.SubscribedEvent{
				SubscriptionType: strp("webhook"),
			},
			Callback: &larkapplication.Callback{
				CallbackType: strp("webhook"),
			},
			OnlineVersionId: strp("online-1"),
		}, nil
	}
	autoConfigGetApplicationVersion = func(context.Context, *FeishuCallBroker, *lark.Client, string, string) (*larkapplication.ApplicationAppVersion, error) {
		return &larkapplication.ApplicationAppVersion{
			VersionId: strp("online-1"),
			Version:   strp("1.0.0"),
			Status:    intp(larkapplication.AppVersionStatusAudited),
			Ability: &larkapplication.AppAbility{
				Bot: &larkapplication.Bot{},
			},
		}, nil
	}
	autoConfigPatchConfig = func(context.Context, *FeishuCallBroker, *lark.Client, string, v7PatchConfigRequest) error {
		return nil
	}

	result, err := ApplyAppAutoConfig(context.Background(), LiveGatewayConfig{GatewayID: "main", AppID: "cli_xxx"}, testAutoConfigManifest(), feishuapp.DefaultFixedPolicy())
	if err != nil {
		t.Fatalf("ApplyAppAutoConfig: %v", err)
	}
	if result.Status != AutoConfigStatusPublishRequired {
		t.Fatalf("apply status = %q, want %q", result.Status, AutoConfigStatusPublishRequired)
	}
	if result.Plan.Diff.ConfigPatchRequired || !result.Plan.Publish.NeedsPublish {
		t.Fatalf("successful weak-write apply must advance to publish: %#v", result.Plan)
	}
	if !reflect.DeepEqual(result.Actions, []AutoConfigAction{{Name: "config_patch", Outcome: "applied"}}) {
		t.Fatalf("unexpected actions: %#v", result.Actions)
	}
}

func TestPublishAppAutoConfigReappliesBeforePublishWhenReadbackLags(t *testing.T) {
	restoreAutoConfigHooks(t)
	var calls []string
	autoConfigListScopes = func(*SetupClient, context.Context) ([]AppScopeStatus, error) {
		return nil, nil
	}
	autoConfigGetApplication = func(context.Context, *FeishuCallBroker, *lark.Client, string) (*larkapplication.Application, error) {
		return &larkapplication.Application{}, nil
	}
	autoConfigGetApplicationVersion = func(context.Context, *FeishuCallBroker, *lark.Client, string, string) (*larkapplication.ApplicationAppVersion, error) {
		return nil, nil
	}
	autoConfigPatchAbility = func(context.Context, *FeishuCallBroker, *lark.Client, string, v7PatchAbilityRequest) error {
		calls = append(calls, "ability")
		return nil
	}
	autoConfigPatchConfig = func(context.Context, *FeishuCallBroker, *lark.Client, string, v7PatchConfigRequest) error {
		calls = append(calls, "config")
		return nil
	}
	autoConfigPublish = func(context.Context, *FeishuCallBroker, *lark.Client, string, v7PublishRequest) (string, string, error) {
		calls = append(calls, "publish")
		return "version-1", "1.0.1", nil
	}

	result, err := PublishAppAutoConfig(context.Background(), LiveGatewayConfig{GatewayID: "main", AppID: "cli_xxx"}, testAutoConfigManifest(), feishuapp.DefaultFixedPolicy(), AutoConfigPublishRequest{})
	if err != nil {
		t.Fatalf("PublishAppAutoConfig: %v", err)
	}
	if !reflect.DeepEqual(calls, []string{"ability", "config", "publish"}) {
		t.Fatalf("calls = %#v", calls)
	}
	if result.Status != AutoConfigStatusAwaitingReview || result.VersionID != "version-1" {
		t.Fatalf("unexpected publish result: %#v", result)
	}
}

func TestPlanAppAutoConfigReturnsUnsupportedPlanInsteadOfError(t *testing.T) {
	restoreAutoConfigHooks(t)
	autoConfigGetApplication = func(context.Context, *FeishuCallBroker, *lark.Client, string) (*larkapplication.Application, error) {
		return nil, &APIError{
			API:  "application.v6.application.get",
			Code: 210015,
			Msg:  "unsupported application",
		}
	}
	autoConfigListScopes = func(*SetupClient, context.Context) ([]AppScopeStatus, error) {
		return nil, nil
	}

	plan, err := PlanAppAutoConfig(
		context.Background(),
		LiveGatewayConfig{GatewayID: "main", AppID: "cli_legacy"},
		testAutoConfigManifest(),
		feishuapp.DefaultFixedPolicy(),
	)
	if err != nil {
		t.Fatalf("PlanAppAutoConfig returned error: %v", err)
	}
	if plan.Status != AutoConfigStatusUnsupported {
		t.Fatalf("plan status = %q, want %q", plan.Status, AutoConfigStatusUnsupported)
	}
	if plan.BlockingReason != autoConfigBlockingUnsupported {
		t.Fatalf("blocking reason = %q, want %q", plan.BlockingReason, autoConfigBlockingUnsupported)
	}
}

func restoreAutoConfigHooks(t *testing.T) {
	t.Helper()
	oldListScopes := autoConfigListScopes
	oldGetApp := autoConfigGetApplication
	oldGetVersion := autoConfigGetApplicationVersion
	oldPatchConfig := autoConfigPatchConfig
	oldPatchAbility := autoConfigPatchAbility
	oldPublish := autoConfigPublish
	t.Cleanup(func() {
		autoConfigListScopes = oldListScopes
		autoConfigGetApplication = oldGetApp
		autoConfigGetApplicationVersion = oldGetVersion
		autoConfigPatchConfig = oldPatchConfig
		autoConfigPatchAbility = oldPatchAbility
		autoConfigPublish = oldPublish
	})
}

func testAutoConfigManifest() feishuapp.Manifest {
	return feishuapp.Manifest{
		Scopes: feishuapp.ScopesImport{
			Scopes: feishuapp.PermissionScopes{
				Tenant: []string{"im:message", "drive:drive"},
			},
		},
		ScopeRequirements: []feishuapp.ScopeRequirement{
			{Scope: "im:message", ScopeType: "tenant", Feature: "core", Required: true},
			{Scope: "drive:drive", ScopeType: "tenant", Feature: "preview", Required: false, DegradeMessage: "markdown preview disabled"},
		},
		Events: []feishuapp.EventRequirement{
			{Event: "im.message.receive_v1", Feature: "core", Required: true},
		},
		Callbacks: []feishuapp.CallbackRequirement{
			{Callback: "card.action.trigger", Feature: "cards", Required: true},
		},
	}
}

func strp(value string) *string {
	return &value
}

func intp(value int) *int {
	return &value
}
