package agentsessions

import (
	"fmt"
	"slices"
	"sort"
)

func UserChannels(msgs []Msg) map[string][]string {
	set := map[string]map[string]bool{}
	for _, m := range msgs {
		if set[m.Author] == nil {
			set[m.Author] = map[string]bool{}
		}
		set[m.Author][m.Channel] = true
	}
	out := make(map[string][]string, len(set))
	for user, channels := range set {
		for c := range channels {
			out[user] = append(out[user], c)
		}
		sort.Strings(out[user])
	}
	return out
}

func VisibleTo(msgs []Msg, channels []string) []Msg {
	var out []Msg
	for _, m := range msgs {
		if slices.Contains(channels, m.Channel) {
			out = append(out, m)
		}
	}
	return out
}

type Window struct {
	User string
	Date string
	Part int
	Msgs []Msg
}

func (w Window) SessionID() string {
	return fmt.Sprintf("%s/%s/s%d", w.User, w.Date, w.Part)
}

func Windows(user string, visible []Msg, maxObs int) []Window {
	var wins []Window
	var current *Window
	for _, m := range visible {
		date := m.At.Format("2006-01-02")
		if current == nil || current.Date != date || len(current.Msgs) >= maxObs {
			part := 1
			if current != nil && current.Date == date {
				part = current.Part + 1
			}
			wins = append(wins, Window{User: user, Date: date, Part: part})
			current = &wins[len(wins)-1]
		}
		current.Msgs = append(current.Msgs, m)
	}
	return wins
}
