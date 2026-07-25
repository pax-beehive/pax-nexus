package onpreme2e_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

const approvalCode = "ORBIT-731"

type coreFlowSuite struct {
	suite.Suite
	baseURL string
	client  *http.Client
	ownerID string
}

func TestCoreFlowSuite(t *testing.T) {
	suite.Run(t, new(coreFlowSuite))
}

func (s *coreFlowSuite) SetupSuite() {
	s.baseURL = strings.TrimRight(os.Getenv("TEAM_MEMORY_E2E_BASE_URL"), "/")
	if s.baseURL == "" {
		s.T().Skip("TEAM_MEMORY_E2E_BASE_URL is not set")
	}
	jar, err := cookiejar.New(nil)
	s.Require().NoError(err)
	s.client = &http.Client{Timeout: 3 * time.Second, Jar: jar}
	s.Require().Eventually(func() bool {
		response, err := s.client.Get(s.baseURL + "/healthz")
		if err != nil {
			return false
		}
		return response.Body.Close() == nil && response.StatusCode == http.StatusOK
	}, 30*time.Second, 100*time.Millisecond, "team-memory did not become healthy")
	login, err := s.client.Get(s.baseURL + "/v1/auth/login")
	s.Require().NoError(err)
	s.Require().NoError(login.Body.Close())
	claimed := s.humanRequest(http.MethodPost, "/v1/bootstrap/claim", nil, map[string]string{
		"X-PAX-Bootstrap-Secret": "e2e-bootstrap-secret",
	})
	s.ownerID = stringField(s.T(), claimed, "user_id")
	s.Equal("owner", stringField(s.T(), claimed, "role"))
	s.Contains(arrayField(s.T(), claimed, "capabilities"), "view.operations")
}

func (s *coreFlowSuite) TestAgentObservationBecomesRecallableTeamNote() {
	producerKey := s.enrollAgent("producer")
	consumerKey := s.enrollAgent("consumer")

	identity := s.request(http.MethodGet, "/v1/agent-identity", consumerKey, nil)
	s.Equal(s.ownerID, stringField(s.T(), identity, "user_id"))
	s.Equal("consumer", stringField(s.T(), identity, "agent_id"))
	s.NotContains(identity, "scope_id")

	receipt := s.request(http.MethodPost, "/v1/observations", producerKey, map[string]any{
		"session_id":      "producer-session",
		"idempotency_key": "e2e-observation-1",
		"complete":        true,
		"events": []map[string]any{{
			"id": "e2e-event-1", "sequence": 1, "type": "assistant",
			"content":     "The team approved the July release with approval code " + approvalCode + ".",
			"task_ref":    "release-42",
			"occurred_at": time.Now().UTC().Format(time.RFC3339Nano),
		}},
	})
	s.InDelta(1, receipt["accepted"], 0)
	s.Equal("processing", stringField(s.T(), receipt, "status"))
	s.Equal("e2e-observation-1", stringField(s.T(), receipt, "idempotency_key"))

	var result map[string]any
	s.Require().Eventually(func() bool {
		result = s.request(http.MethodPost, "/v1/memory/search", consumerKey, map[string]any{
			"intent": "passive", "session_id": "consumer-session", "task_ref": "release-42",
			"query": "What approval code was approved for the July release?", "token_budget": 128, "max_items": 5,
		})
		return responseContainsEvidence(result, approvalCode)
	}, 30*time.Second, 250*time.Millisecond, "observation never became recallable Team Note evidence")

	s.True(boolField(s.T(), result, "evidence_sufficient"))
	trace := objectField(s.T(), result, "trace")
	teamNoteTrace := objectField(s.T(), trace, "team_note")
	s.Equal("completed", stringField(s.T(), teamNoteTrace, "status"))

	s.assertOperationsFlow()
}

func (s *coreFlowSuite) TestKnowledgeCapsuleChannelEndToEnd() {
	senderKey := s.enrollAgentWithPermissions("channel-sender", []string{"channel_send"})
	recipientKey := s.enrollAgentWithPermissions("channel-recipient", []string{"channel_receive"})
	otherRecipientKey := s.enrollAgentWithPermissions("channel-other", []string{"channel_receive"})
	directory := s.request(http.MethodGet, "/v1/channel/agents?q=channel-recipient", senderKey, nil)
	directoryAgents := arrayField(s.T(), directory, "agents")
	s.Require().Len(directoryAgents, 1)
	directoryAgent, ok := directoryAgents[0].(map[string]any)
	s.Require().True(ok)
	s.Equal("channel-recipient", stringField(s.T(), directoryAgent, "agent_id"))
	s.NotContains(directoryAgent, "owner_user_id")
	fetchedAgent := s.request(http.MethodGet, "/v1/channel/agents/channel-recipient", senderKey, nil)
	s.Equal("channel-recipient", stringField(s.T(), objectField(s.T(), fetchedAgent, "agent"), "agent_id"))
	s.expectStatus(http.StatusForbidden, http.MethodGet, "/v1/channel/agents", recipientKey, nil)
	payload := map[string]any{
		"schema_version": "paxl.envelope_payload.knowledge_capsule.v2",
		"capsule": map[string]any{
			"capsule_id": "kcap-e2e", "source_session_id": "codex:e2e-source",
			"source_agent": "codex", "keyword": "onprem", "title": "On-prem handoff",
			"summary": "Channel delivery", "content": "The capsule crossed the on-prem channel.",
			"status": "active", "truncated": false, "original_estimated_chars": 44,
		},
		"route": map[string]any{
			"match_type": "project", "match_value": "team-memory", "target_agent": "codex",
		},
	}
	request := map[string]any{
		"to_agent_id": "channel-recipient", "payload_type": "knowledge_capsule",
		"payload_json": payload, "message": "review", "idempotency_key": "channel-e2e-1",
	}

	created := s.request(http.MethodPost, "/v1/channel/envelopes", senderKey, request)
	envelope := objectField(s.T(), created, "envelope")
	envelopeID := stringField(s.T(), envelope, "envelope_id")
	s.Equal("channel-sender", stringField(s.T(), envelope, "from_agent_id"))
	s.Equal("channel-recipient", stringField(s.T(), envelope, "to_agent_id"))
	s.Equal("pending", stringField(s.T(), envelope, "status"))

	replayed := s.request(http.MethodPost, "/v1/channel/envelopes", senderKey, request)
	s.Equal(envelopeID, stringField(s.T(), objectField(s.T(), replayed, "envelope"), "envelope_id"))
	request["message"] = "different intent"
	s.expectStatus(http.StatusConflict, http.MethodPost, "/v1/channel/envelopes", senderKey, request)
	request["message"] = "review"

	inbox := s.request(http.MethodGet, "/v1/channel/envelopes?status=pending", recipientKey, nil)
	envelopes := arrayField(s.T(), inbox, "envelopes")
	s.Require().Len(envelopes, 1)
	inboxEnvelope, ok := envelopes[0].(map[string]any)
	s.Require().True(ok)
	s.Equal(envelopeID, stringField(s.T(), inboxEnvelope, "envelope_id"))
	expectedPayload, err := json.Marshal(payload)
	s.Require().NoError(err)
	actualPayload, err := json.Marshal(objectField(s.T(), inboxEnvelope, "payload_json"))
	s.Require().NoError(err)
	s.JSONEq(string(expectedPayload), string(actualPayload))

	fetched := s.request(http.MethodGet, "/v1/channel/envelopes/"+envelopeID, recipientKey, nil)
	s.Equal(envelopeID, stringField(s.T(), objectField(s.T(), fetched, "envelope"), "envelope_id"))
	s.expectStatus(http.StatusForbidden, http.MethodGet, "/v1/channel/envelopes/"+envelopeID, senderKey, nil)
	s.expectStatus(http.StatusNotFound, http.MethodGet, "/v1/channel/envelopes/"+envelopeID, otherRecipientKey, nil)

	accepted := s.request(http.MethodPost, "/v1/channel/envelopes/"+envelopeID+"/accept", recipientKey, nil)
	s.Equal("accepted", stringField(s.T(), objectField(s.T(), accepted, "envelope"), "status"))

	acceptedInbox := s.request(http.MethodGet, "/v1/channel/envelopes?status=accepted", recipientKey, nil)
	s.Require().Len(arrayField(s.T(), acceptedInbox, "envelopes"), 1)

	archived := s.request(http.MethodPost, "/v1/channel/envelopes/"+envelopeID+"/archive", recipientKey, nil)
	s.Equal("archived", stringField(s.T(), objectField(s.T(), archived, "envelope"), "status"))

	outbox := s.request(http.MethodGet, "/v1/channel/envelopes?direction=sent&status=archived", senderKey, nil)
	s.Require().Len(arrayField(s.T(), outbox, "envelopes"), 1)
	archivedInbox := s.request(http.MethodGet, "/v1/channel/envelopes?status=archived", recipientKey, nil)
	s.Require().Len(arrayField(s.T(), archivedInbox, "envelopes"), 1)
	s.expectStatus(http.StatusForbidden, http.MethodGet, "/v1/channel/envelopes", senderKey, nil)
	s.expectStatus(http.StatusForbidden, http.MethodGet, "/v1/channel/envelopes?direction=sent", recipientKey, nil)
}

func (s *coreFlowSuite) TestDeviceProvisioningEndToEnd() {
	enrollment := s.humanRequest(http.MethodPost, "/v1/me/device-enrollments", map[string]any{
		"device_name": "e2e-device", "grantable_permissions": []string{"observe", "search", "get"},
		"expires_in_seconds": 300,
	}, nil)
	s.Equal("e2e-device", stringField(s.T(), enrollment, "device_name"))
	deviceCredential := s.request(http.MethodPost, "/v1/agent-enrollments/exchange", "", map[string]any{
		"token": stringField(s.T(), enrollment, "token"),
	})
	deviceKey := stringField(s.T(), deviceCredential, "api_key")
	deviceCredentialID := stringField(s.T(), deviceCredential, "credential_id")
	s.Equal(s.ownerID, stringField(s.T(), deviceCredential, "user_id"))
	s.Equal("device", stringField(s.T(), deviceCredential, "kind"))
	devicePermissions := arrayField(s.T(), deviceCredential, "permissions")
	s.Require().Len(devicePermissions, 1)
	s.Equal("agent_provision", devicePermissions[0])

	// A device credential is structurally forbidden from the knowledge plane.
	s.expectStatus(http.StatusForbidden, http.MethodPost, "/v1/observations", deviceKey, map[string]any{
		"session_id": "device-session", "idempotency_key": "e2e-device-observe", "complete": true,
		"events": []map[string]any{{
			"id": "e2e-device-event", "sequence": 1, "type": "assistant",
			"content": "device credentials must not observe", "occurred_at": time.Now().UTC().Format(time.RFC3339Nano),
		}},
	})

	// Provision an agent; re-provisioning the same agent rotates its credential.
	provisioned := s.provisionDeviceAgent(deviceKey, "device-agent")
	s.True(boolField(s.T(), provisioned, "agent_created"))
	s.Equal(s.ownerID, stringField(s.T(), provisioned, "user_id"))
	firstAgentKey := stringField(s.T(), provisioned, "api_key")

	rotated := s.provisionDeviceAgent(deviceKey, "device-agent")
	s.False(boolField(s.T(), rotated, "agent_created"))
	s.NotEmpty(stringField(s.T(), rotated, "rotated_from_credential_id"))
	s.expectStatus(http.StatusUnauthorized, http.MethodGet, "/v1/agent-identity", firstAgentKey, nil)
	agentKey := stringField(s.T(), rotated, "api_key")

	// A human-registered agent cannot be claimed by a device.
	s.humanRequest(http.MethodPost, "/v1/me/agents", map[string]any{
		"agent_id": "device-conflict-agent", "display_name": "device-conflict-agent",
		"description": "human-registered agent", "agent_type": "test", "directory_visible": false,
	}, map[string]string{"Idempotency-Key": "create-device-conflict-agent"})
	conflictStatus, conflictBody := s.performRequest(http.MethodPost, "/v1/device/agent-provisions", deviceKey, map[string]any{
		"agent_id": "device-conflict-agent", "display_name": "device-conflict-agent", "agent_type": "codex",
	})
	s.Equal(http.StatusConflict, conflictStatus, string(conflictBody))

	// The provisioned agent credential works on the knowledge plane.
	identity := s.request(http.MethodGet, "/v1/agent-identity", agentKey, nil)
	s.Equal("device-agent", stringField(s.T(), identity, "agent_id"))
	s.Equal(s.ownerID, stringField(s.T(), identity, "user_id"))
	receipt := s.request(http.MethodPost, "/v1/observations", agentKey, map[string]any{
		"session_id": "device-agent-session", "idempotency_key": "e2e-device-agent-observe", "complete": true,
		"events": []map[string]any{{
			"id": "e2e-device-agent-event", "sequence": 1, "type": "assistant",
			"content": "device-provisioned agents can observe", "occurred_at": time.Now().UTC().Format(time.RFC3339Nano),
		}},
	})
	s.InDelta(1, receipt["accepted"], 0)

	// The device lists its provisions (rotation history included); admin views show the device.
	provisions := s.request(http.MethodGet, "/v1/device/agent-provisions", deviceKey, nil)
	provisionedAgents := arrayField(s.T(), provisions, "agents")
	s.GreaterOrEqual(len(provisionedAgents), 2)
	for _, value := range provisionedAgents {
		agent, ok := value.(map[string]any)
		s.Require().True(ok)
		s.Equal("device-agent", stringField(s.T(), agent, "agent_id"))
	}

	devices := s.humanRequest(http.MethodGet, "/v1/admin/devices", nil, nil)
	var deviceSummary map[string]any
	for _, value := range arrayField(s.T(), devices, "devices") {
		candidate, ok := value.(map[string]any)
		s.Require().True(ok)
		if stringField(s.T(), candidate, "credential_id") == deviceCredentialID {
			deviceSummary = candidate
		}
	}
	s.Require().NotNil(deviceSummary, "admin device list must contain the e2e device")
	s.Equal("e2e-device", stringField(s.T(), deviceSummary, "device_name"))
	s.Equal("active", stringField(s.T(), deviceSummary, "status"))
	s.GreaterOrEqual(intField(s.T(), deviceSummary, "provisioned_agent_count"), int64(1))
	detail := s.humanRequest(http.MethodGet, "/v1/admin/devices/"+deviceCredentialID, nil, nil)
	s.Equal(deviceCredentialID, stringField(s.T(), objectField(s.T(), detail, "device"), "credential_id"))

	// Cascade revocation disables the device and every credential it provisioned.
	revoked := s.humanRequest(http.MethodDelete, "/v1/admin/devices/"+deviceCredentialID, nil,
		map[string]string{"Idempotency-Key": "revoke-e2e-device"})
	s.Equal("revoked", stringField(s.T(), objectField(s.T(), revoked, "device"), "status"))
	s.expectStatus(http.StatusUnauthorized, http.MethodGet, "/v1/agent-identity", agentKey, nil)
	s.expectStatus(http.StatusUnauthorized, http.MethodPost, "/v1/device/agent-provisions", deviceKey, map[string]any{
		"agent_id": "device-agent-late", "display_name": "device-agent-late", "agent_type": "codex",
	})
}

func (s *coreFlowSuite) provisionDeviceAgent(deviceKey string, agentID string) map[string]any {
	s.T().Helper()
	return s.request(http.MethodPost, "/v1/device/agent-provisions", deviceKey, map[string]any{
		"agent_id": agentID, "display_name": agentID, "agent_type": "codex",
		"permissions": []string{"observe", "search", "get"},
	})
}

func (s *coreFlowSuite) enrollAgent(agentID string) string {
	return s.enrollAgentWithPermissions(agentID, []string{
		"observe", "search", "get", "channel_send", "channel_receive",
	})
}

func (s *coreFlowSuite) enrollAgentWithPermissions(agentID string, permissions []string) string {
	s.humanRequest(http.MethodPost, "/v1/me/agents", map[string]any{
		"agent_id": agentID, "display_name": agentID, "description": "on-prem E2E agent",
		"agent_type": "test", "directory_visible": true,
	}, map[string]string{"Idempotency-Key": "create-" + agentID})
	enrollment := s.humanRequest(http.MethodPost, "/v1/me/agents/"+agentID+"/enrollments", map[string]any{
		"credential_label": "e2e", "permissions": permissions, "expires_in_seconds": 300,
	}, nil)
	token := stringField(s.T(), enrollment, "token")
	segments := strings.Split(token, ".")
	s.Require().Len(segments, 3)
	origin, err := base64.RawURLEncoding.DecodeString(segments[2])
	s.Require().NoError(err)
	s.Equal("http://team-memory:8080/", string(origin))
	credential := s.request(http.MethodPost, "/v1/agent-enrollments/exchange", "", map[string]any{"token": token})
	return stringField(s.T(), credential, "api_key")
}

func (s *coreFlowSuite) assertOperationsFlow() {
	s.T().Helper()
	summary := s.humanRequest(http.MethodGet, "/v1/admin/operations/summary", nil, nil)
	s.GreaterOrEqual(intField(s.T(), objectField(s.T(), summary, "observations"), "requests"), int64(1))
	s.GreaterOrEqual(intField(s.T(), objectField(s.T(), summary, "recalls"), "memory_search_requests"), int64(1))
	s.GreaterOrEqual(intField(s.T(), objectField(s.T(), summary, "extraction"), "completed"), int64(1))

	extractions := s.humanRequest(http.MethodGet, "/v1/admin/operations/events?operation_kind=extraction.run", nil, nil)
	extractionEvents := arrayField(s.T(), extractions, "events")
	s.NotEmpty(extractionEvents)
	for _, value := range extractionEvents {
		event, ok := value.(map[string]any)
		s.Require().True(ok)
		s.Equal("extraction.run", stringField(s.T(), event, "operation_kind"))
		s.Equal("extraction_run", stringField(s.T(), event, "detail_kind"))
	}
	searches := s.humanRequest(http.MethodGet, "/v1/admin/operations/events?operation_kind=memory.search", nil, nil)
	diagnosticID := operationDetailID(s.T(), arrayField(s.T(), searches, "events"), "memory.search", "recall_observation")
	diagnostic := s.humanRequest(http.MethodGet, "/v1/admin/operations/recalls/"+diagnosticID, nil, nil)
	encoded, err := json.Marshal(diagnostic)
	s.Require().NoError(err)
	s.NotContains(string(encoded), approvalCode)
	s.Contains(string(encoded), `"candidates"`)

	storage := objectField(s.T(), s.humanRequest(http.MethodGet, "/v1/admin/operations/storage", nil, nil), "storage")
	s.NotEmpty(arrayField(s.T(), storage, "components"))
}

func (s *coreFlowSuite) humanRequest(method, path string, body any, headers map[string]string) map[string]any {
	s.T().Helper()
	statusCode, responseBody := s.performHumanRequest(method, path, body, headers)
	s.Require().GreaterOrEqual(statusCode, http.StatusOK, string(responseBody))
	s.Require().Less(statusCode, http.StatusMultipleChoices, string(responseBody))
	result := make(map[string]any)
	s.Require().NoError(json.Unmarshal(responseBody, &result), "decode %s %s response", method, path)
	return result
}

func (s *coreFlowSuite) performHumanRequest(
	method string,
	path string,
	body any,
	headers map[string]string,
) (int, []byte) {
	s.T().Helper()
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		s.Require().NoError(err)
		requestBody = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(context.Background(), method, s.baseURL+path, requestBody)
	s.Require().NoError(err)
	request.Header.Set("Content-Type", "application/json")
	if method != http.MethodGet && method != http.MethodHead {
		request.Header.Set("X-CSRF-Token", s.cookieValue("tm_csrf"))
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := s.client.Do(request)
	s.Require().NoError(err)
	responseBody, err := io.ReadAll(response.Body)
	s.Require().NoError(err)
	s.Require().NoError(response.Body.Close())
	return response.StatusCode, responseBody
}

func (s *coreFlowSuite) cookieValue(name string) string {
	s.T().Helper()
	parsed, err := url.Parse(s.baseURL)
	s.Require().NoError(err)
	for _, cookie := range s.client.Jar.Cookies(parsed) {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	s.T().Fatalf("cookie %s is missing", name)
	return ""
}

func (s *coreFlowSuite) request(method, path, apiKey string, body any) map[string]any {
	s.T().Helper()
	statusCode, responseBody := s.performRequest(method, path, apiKey, body)
	s.Require().GreaterOrEqual(statusCode, http.StatusOK, string(responseBody))
	s.Require().Less(statusCode, http.StatusMultipleChoices, string(responseBody))
	result := make(map[string]any)
	s.Require().NoError(json.Unmarshal(responseBody, &result), "decode %s %s response", method, path)
	return result
}

func (s *coreFlowSuite) expectStatus(expected int, method, path, apiKey string, body any) {
	s.T().Helper()
	statusCode, responseBody := s.performRequest(method, path, apiKey, body)
	s.Equal(expected, statusCode, string(responseBody))
}

func (s *coreFlowSuite) performRequest(method, path, apiKey string, body any) (int, []byte) {
	s.T().Helper()
	var requestBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		s.Require().NoError(err)
		requestBody = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(context.Background(), method, s.baseURL+path, requestBody)
	s.Require().NoError(err)
	request.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+apiKey)
	}
	response, err := s.client.Do(request)
	s.Require().NoError(err)
	responseBody, err := io.ReadAll(response.Body)
	s.Require().NoError(err)
	s.Require().NoError(response.Body.Close())
	return response.StatusCode, responseBody
}

func responseContainsEvidence(response map[string]any, expected string) bool {
	hits, ok := response["hits"].([]any)
	if !ok {
		return false
	}
	for _, value := range hits {
		hit, ok := value.(map[string]any)
		if ok && hit["disposition"] == "evidence" && strings.Contains(fmt.Sprint(hit["text"]), expected) {
			return true
		}
	}
	return false
}

func objectField(t *testing.T, value map[string]any, name string) map[string]any {
	t.Helper()
	result, ok := value[name].(map[string]any)
	if !ok {
		t.Fatalf("field %s is not an object: %#v", name, value[name])
	}
	return result
}

func stringField(t *testing.T, value map[string]any, name string) string {
	t.Helper()
	result, ok := value[name].(string)
	if !ok {
		t.Fatalf("field %s is not a string: %#v", name, value[name])
	}
	return result
}

func boolField(t *testing.T, value map[string]any, name string) bool {
	t.Helper()
	result, ok := value[name].(bool)
	if !ok {
		t.Fatalf("field %s is not a bool: %#v", name, value[name])
	}
	return result
}

func intField(t *testing.T, value map[string]any, name string) int64 {
	t.Helper()
	result, ok := value[name].(float64)
	if !ok {
		t.Fatalf("field %s is not a number: %#v", name, value[name])
	}
	return int64(result)
}

func arrayField(t *testing.T, value map[string]any, name string) []any {
	t.Helper()
	result, ok := value[name].([]any)
	if !ok {
		t.Fatalf("field %s is not an array: %#v", name, value[name])
	}
	return result
}

func operationDetailID(t *testing.T, events []any, operationKind string, detailKind string) string {
	t.Helper()
	for _, value := range events {
		event, ok := value.(map[string]any)
		if ok && event["operation_kind"] == operationKind && event["detail_kind"] == detailKind && fmt.Sprint(event["detail_id"]) != "" {
			return fmt.Sprint(event["detail_id"])
		}
	}
	t.Fatalf("%s operation events have no %s detail: %#v", operationKind, detailKind, events)
	return ""
}
