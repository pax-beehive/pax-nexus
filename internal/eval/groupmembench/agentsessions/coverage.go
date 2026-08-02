package agentsessions

type CoverageException struct {
	QuestionID string `json:"question_id"`
	Reason     string `json:"reason"`
	MsgNode    string `json:"msg_node"`
}

func VerifyCoverage(questions []EnhancedQuestion, sessions []Session) []CoverageException {
	observed := map[string]map[string]bool{}  // user → msg_node → seen
	memorized := map[string]map[string]bool{} // user → msg_node → memory_write
	for _, s := range sessions {
		user := s.Agent.UserID
		if observed[user] == nil {
			observed[user] = map[string]bool{}
			memorized[user] = map[string]bool{}
		}
		for _, o := range s.Observations {
			observed[user][o.MsgNode] = true
		}
		for _, action := range s.Trajectory {
			if action.Type != "memory_write" {
				continue
			}
			for _, node := range action.SourceMsgs {
				memorized[user][node] = true
			}
		}
	}
	var exceptions []CoverageException
	for _, q := range questions {
		if q.Category == "abstention" {
			continue
		}
		for _, node := range q.EvidenceMsgIDs {
			switch {
			case !observed[q.AskingUserID][node]:
				exceptions = append(exceptions, CoverageException{
					QuestionID: q.ID, Reason: "evidence_not_observed", MsgNode: node})
			case !memorized[q.AskingUserID][node]:
				exceptions = append(exceptions, CoverageException{
					QuestionID: q.ID, Reason: "no_memory_write", MsgNode: node})
			}
		}
	}
	return exceptions
}
