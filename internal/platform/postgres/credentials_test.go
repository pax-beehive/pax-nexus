package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/pax-beehive/pax-nexus/internal/deployment/onprem"
	"github.com/pax-beehive/pax-nexus/internal/platform/postgres"
	"github.com/stretchr/testify/require"
)

func TestCredentialStoreLifecycle(t *testing.T) {
	store, err := postgres.Open(context.Background(), testDSN(t))
	require.NoError(t, err)
	defer store.Close()
	require.NoError(t, store.Migrate(context.Background()))
	service, err := onprem.NewCredentialService(store.Credentials(), onprem.CredentialConfig{
		AdminAPIKey: "postgres-admin", RotationOverlap: time.Minute,
	})
	require.NoError(t, err)
	ctx := context.Background()
	admin, err := service.Authenticate(ctx, "postgres-admin")
	require.NoError(t, err)
	enrollment, err := service.CreateEnrollment(ctx, admin, onprem.EnrollmentRequest{
		UserID: "postgres-user", AgentID: "postgres-agent", ExpiresIn: time.Minute,
	})
	require.NoError(t, err)
	credential, err := service.ExchangeEnrollment(ctx, enrollment.Token)
	require.NoError(t, err)
	principal, err := service.Authenticate(ctx, credential.APIKey)
	require.NoError(t, err)
	require.Equal(t, "postgres-agent", principal.AgentID)
	require.NoError(t, service.RevokeCredential(ctx, admin, principal.CredentialID))
	_, err = service.Authenticate(ctx, credential.APIKey)
	require.ErrorIs(t, err, onprem.ErrUnauthorized)
}
