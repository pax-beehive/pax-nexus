package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/suite"
)

type commandSuite struct{ suite.Suite }

func TestCommandSuite(t *testing.T) { suite.Run(t, new(commandSuite)) }

func (s *commandSuite) TestRejectsInvalidInvocationBeforeNetworkUse() {
	environment := map[string]string{
		"TEAM_MEMORY_API_KEY": "key", "PAXM_USER_ID": "user", "PAXM_AGENT_ID": "agent", "MEM0_RUN_ID": "run",
	}
	getenv := func(name string) string { return environment[name] }
	tests := []struct {
		name string
		args []string
	}{
		{name: "missing action"},
		{name: "missing ingest file", args: []string{"-action", "ingest", "-provider", "mem0"}},
		{name: "bad flags", args: []string{"-unknown"}},
	}
	for _, test := range tests {
		s.Run(test.name, func() {
			s.Error(run(context.Background(), test.args, getenv))
		})
	}
}
