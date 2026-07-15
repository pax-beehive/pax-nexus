package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/suite"
)

type environmentSuite struct {
	suite.Suite
}

func TestEnvironmentSuite(t *testing.T) {
	suite.Run(t, new(environmentSuite))
}

func (s *environmentSuite) TestDurationEnv() {
	tests := []struct {
		name    string
		value   string
		want    time.Duration
		wantErr bool
	}{
		{name: "unset"},
		{name: "valid", value: "45s", want: 45 * time.Second},
		{name: "invalid", value: "later", wantErr: true},
		{name: "zero", value: "0s", wantErr: true},
		{name: "negative", value: "-1s", wantErr: true},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			name := "TEAM_MEMORY_TEST_REQUEST_TIMEOUT"
			s.T().Setenv(name, test.value)
			got, err := durationEnv(name)
			if test.wantErr {
				s.Require().Error(err)
				return
			}
			s.Require().NoError(err)
			s.Equal(test.want, got)
		})
	}
}
