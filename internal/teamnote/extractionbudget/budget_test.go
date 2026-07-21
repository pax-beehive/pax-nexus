package extractionbudget_test

import (
	"math"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/teamnote/extractionbudget"
	"github.com/stretchr/testify/suite"
)

type budgetSuite struct {
	suite.Suite
}

func (s *budgetSuite) TestEnvelopeRejectsOverflowingSerialBudget() {
	envelope := extractionbudget.DefaultEnvelope()
	envelope.MaxSlicesPerJob = math.MaxInt

	s.Error(envelope.Validate())
}

func (s *budgetSuite) TestJobProviderBudgetSaturatesForZeroAttemptTimeout() {
	envelope := extractionbudget.DefaultEnvelope()
	envelope.Provider.AttemptTimeout = 0

	s.Equal(time.Duration(math.MaxInt64), envelope.JobProviderBudget())
	s.Require().Error(envelope.Validate())
}

func (s *budgetSuite) TestProviderRejectsBudgetWithoutPersistenceMargin() {
	policy := extractionbudget.DefaultProviderPolicy()
	policy.AttemptTimeout = time.Duration(math.MaxInt64)

	s.Require().Error(policy.Validate())
	s.Equal(time.Duration(math.MaxInt64), policy.BackgroundCallTimeout())
}

func TestBudgetSuite(t *testing.T) {
	t.Parallel()
	suite.Run(t, new(budgetSuite))
}

func (s *budgetSuite) TestDefaultEnvelopeFitsOneProviderCallInsideWorkerJob() {
	envelope := extractionbudget.DefaultEnvelope()

	s.Require().NoError(envelope.Validate())
	s.Equal(120*time.Second, envelope.Provider.LogicalCallBudget())
	s.Equal(120*time.Second, envelope.JobProviderBudget())
	s.Equal(130*time.Second, envelope.Provider.BackgroundCallTimeout())
	s.Equal(3*time.Minute, envelope.WorkerJobTimeout)
	s.Equal(1, envelope.MaxSlicesPerJob)
}

func (s *budgetSuite) TestNormalizeProviderPolicyAppliesSharedDefaults() {
	policy, err := extractionbudget.NormalizeProviderPolicy(extractionbudget.ProviderPolicy{})

	s.Require().NoError(err)
	s.Equal(extractionbudget.DefaultProviderPolicy(), policy)
}

func (s *budgetSuite) TestEnvelopeValidationMatrix() {
	tests := []struct {
		name       string
		envelope   extractionbudget.Envelope
		wantBudget time.Duration
		wantError  bool
	}{
		{
			name: "compaction exceeds default worker deadline",
			envelope: func() extractionbudget.Envelope {
				value := extractionbudget.DefaultEnvelope()
				value.CompactionEnabled = true
				return value
			}(),
			wantBudget: 6 * time.Minute, wantError: true,
		},
		{
			name: "compaction fits expanded worker deadline",
			envelope: func() extractionbudget.Envelope {
				value := extractionbudget.DefaultEnvelope()
				value.CompactionEnabled = true
				value.WorkerJobTimeout = 7 * time.Minute
				return value
			}(),
			wantBudget: 6 * time.Minute,
		},
		{
			name: "retry backoff contributes to every slice",
			envelope: extractionbudget.Envelope{
				Provider: extractionbudget.ProviderPolicy{
					AttemptTimeout: time.Minute, MaxAttempts: 2, RetryBackoff: time.Second,
					MaxResponseBytes: 1, PrimaryMaxOutputTokens: 1,
					SummaryMaxOutputTokens: 1, CompactionMaxOutputTokens: 1,
				},
				MaxSlicesPerJob: 2, WorkerJobTimeout: 5 * time.Minute,
			},
			wantBudget: 242 * time.Second,
		},
		{
			name: "persistence margin may not reach worker deadline",
			envelope: extractionbudget.Envelope{
				Provider: extractionbudget.ProviderPolicy{
					AttemptTimeout: 170 * time.Second, MaxAttempts: 1,
					MaxResponseBytes: 1, PrimaryMaxOutputTokens: 1,
					SummaryMaxOutputTokens: 1, CompactionMaxOutputTokens: 1,
				},
				MaxSlicesPerJob: 1, WorkerJobTimeout: 3 * time.Minute,
			},
			wantBudget: 170 * time.Second, wantError: true,
		},
		{
			name: "persistence margin fits below worker deadline",
			envelope: extractionbudget.Envelope{
				Provider: extractionbudget.ProviderPolicy{
					AttemptTimeout: 169 * time.Second, MaxAttempts: 1,
					MaxResponseBytes: 1, PrimaryMaxOutputTokens: 1,
					SummaryMaxOutputTokens: 1, CompactionMaxOutputTokens: 1,
				},
				MaxSlicesPerJob: 1, WorkerJobTimeout: 3 * time.Minute,
			},
			wantBudget: 169 * time.Second,
		},
	}

	for _, test := range tests {
		s.Run(test.name, func() {
			s.Equal(test.wantBudget, test.envelope.JobProviderBudget())
			err := test.envelope.Validate()
			if test.wantError {
				s.Error(err)
				return
			}
			s.NoError(err)
		})
	}
}
