package onpreme2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
}

func TestCoreFlowSuite(t *testing.T) {
	suite.Run(t, new(coreFlowSuite))
}

func (s *coreFlowSuite) SetupSuite() {
	s.baseURL = strings.TrimRight(os.Getenv("TEAM_MEMORY_E2E_BASE_URL"), "/")
	if s.baseURL == "" {
		s.T().Skip("TEAM_MEMORY_E2E_BASE_URL is not set")
	}
	s.client = &http.Client{Timeout: 3 * time.Second}
	s.Require().Eventually(func() bool {
		response, err := s.client.Get(s.baseURL + "/healthz")
		if err != nil {
			return false
		}
		return response.Body.Close() == nil && response.StatusCode == http.StatusOK
	}, 30*time.Second, 100*time.Millisecond, "team-memory did not become healthy")
}

func (s *coreFlowSuite) TestAgentObservationBecomesRecallableTeamNote() {
	producerKey := s.enrollAgent("producer")
	consumerKey := s.enrollAgent("consumer")

	identity := s.request(http.MethodGet, "/v1/agent-identity", consumerKey, nil)
	s.Equal("e2e-owner", stringField(s.T(), identity, "user_id"))
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
}

func (s *coreFlowSuite) TestAgentsShareKnowledgeCapsuleThroughChannel() {
	senderKey := s.enrollAgent("channel-sender")
	recipientKey := s.enrollAgent("channel-recipient")
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

	accepted := s.request(http.MethodPost, "/v1/channel/envelopes/"+envelopeID+"/accept", recipientKey, nil)
	s.Equal("accepted", stringField(s.T(), objectField(s.T(), accepted, "envelope"), "status"))

	outbox := s.request(http.MethodGet, "/v1/channel/envelopes?direction=sent&status=accepted", senderKey, nil)
	s.Require().Len(arrayField(s.T(), outbox, "envelopes"), 1)
}

func (s *coreFlowSuite) enrollAgent(agentID string) string {
	enrollment := s.request(http.MethodPost, "/v1/admin/agent-enrollments", "e2e-admin-secret", map[string]any{
		"user_id": "e2e-owner", "agent_id": agentID, "expires_in_seconds": 300,
		"permissions": []string{"observe", "search", "get", "channel_send", "channel_receive"},
	})
	token := stringField(s.T(), enrollment, "token")
	credential := s.request(http.MethodPost, "/v1/agent-enrollments/exchange", "", map[string]any{"token": token})
	return stringField(s.T(), credential, "api_key")
}

func (s *coreFlowSuite) request(method, path, apiKey string, body any) map[string]any {
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
	s.Require().GreaterOrEqual(response.StatusCode, http.StatusOK, string(responseBody))
	s.Require().Less(response.StatusCode, http.StatusMultipleChoices, string(responseBody))
	result := make(map[string]any)
	s.Require().NoError(json.Unmarshal(responseBody, &result), "decode %s %s response", method, path)
	return result
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

func arrayField(t *testing.T, value map[string]any, name string) []any {
	t.Helper()
	result, ok := value[name].([]any)
	if !ok {
		t.Fatalf("field %s is not an array: %#v", name, value[name])
	}
	return result
}
