package server

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-lookout/internal/ai"
	"github.com/abjelosevic88/pocket-homelab-lookout/internal/store"
)

// ---- AI: change summaries and "only if" conditions ----
//
// Pocket AI through Core (the person's own AI settings there) or an
// OpenAI-compatible API with the person's own key. Without either, changes
// get a plain summary and conditions count every change.

func (s *Server) aiFor(ctx context.Context, uid int64, st store.Settings) (*ai.Client, string, string) {
	email := ""
	if u, err := s.store.GetUser(ctx, uid); err == nil {
		email = u.Email
	}
	pocket, status := s.core.SelectAI(ctx, email, st.AIProvider, st.OpenAIKey != "")
	if pocket != nil {
		if !status.Ready {
			return nil, "", "Pocket AI: " + status.Detail
		}
		c := ai.New("", "", "", 120*time.Second)
		c.Pocket = pocket.WithModel(st.AIPocketModel)
		return c, "pocket", ""
	}
	if st.OpenAIKey == "" {
		why := status.Detail
		if why == "" {
			why = "no AI is set up"
		}
		return nil, "", why
	}
	return ai.New(st.OpenAIBaseURL, st.OpenAIKey, st.OpenAIModel, 120*time.Second), "openai", ""
}

func aiJSON(ctx context.Context, c *ai.Client, system, prompt string, v any) error {
	txt, err := c.Complete(ctx, system, prompt, true)
	if err != nil {
		return err
	}
	if i, j := strings.Index(txt, "{"), strings.LastIndex(txt, "}"); i >= 0 && j > i {
		txt = txt[i : j+1]
	}
	if err := json.Unmarshal([]byte(txt), v); err != nil {
		return fmt.Errorf("the AI's answer was not valid JSON: %w", err)
	}
	return nil
}

type aiVerdict struct {
	Summary string `json:"summary"`
	Match   *bool  `json:"match"`
	Reason  string `json:"reason"`
}

const judgeSystem = `You tell a busy, non-technical person what changed on a web page they watch.
You get the page's name and address, the lines that were added and the lines that went away.
Answer with JSON only: {"summary": "...", "match": true|false, "reason": "..."}.
- summary: ONE short plain sentence (at most 140 characters) saying what actually changed, with the concrete values (prices, dates, names, counts). No "the page was updated". Write in English unless the page's lines are in another language and the person's condition is too.
- match and reason: only when a condition is given. match is true when the change satisfies the condition, judged from the lines alone; reason is one short sentence why (with the value that decided it). Without a condition use "match": true and "reason": "".`

func capLines(ls []string, maxLines, maxChars int) string {
	var b strings.Builder
	for i, l := range ls {
		if i >= maxLines || b.Len() > maxChars {
			fmt.Fprintf(&b, "… (%d more lines)\n", len(ls)-i)
			break
		}
		b.WriteString(oneLine(l, 400))
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		return "(none)\n"
	}
	return b.String()
}

func (s *Server) aiJudge(ctx context.Context, c *ai.Client, w *store.Watch, added, removed []string) (*aiVerdict, error) {
	var p strings.Builder
	fmt.Fprintf(&p, "Page: %s\nAddress: %s\n", w.Name, w.URL)
	if w.Selector != "" {
		fmt.Fprintf(&p, "Watched part of the page: %s\n", strings.ReplaceAll(w.Selector, "\n", ", "))
	}
	if cond := strings.TrimSpace(w.AICondition); cond != "" {
		fmt.Fprintf(&p, "Condition: %s\n", cond)
	} else {
		p.WriteString("Condition: none\n")
	}
	p.WriteString("\nLines added:\n")
	p.WriteString(capLines(added, 80, 4000))
	p.WriteString("\nLines that went away:\n")
	p.WriteString(capLines(removed, 80, 4000))
	var v aiVerdict
	if err := aiJSON(ctx, c, judgeSystem, p.String(), &v); err != nil {
		return nil, err
	}
	return &v, nil
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		s = string(r[:n]) + "…"
	}
	return s
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func (s *Server) handleAIStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	st, _ := s.store.GetSettings(ctx, actorID(ctx))
	cl, used, why := s.aiFor(ctx, actorID(ctx), st)
	writeJSON(w, http.StatusOK, map[string]any{"ready": cl != nil, "provider": used, "detail": why})
}

func (s *Server) handleAIModels(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	email := userFrom(ctx).Email
	c, status := s.core.SelectAI(ctx, email, "pocket", false)
	if c == nil || !status.Ready {
		writeErr(w, http.StatusServiceUnavailable, status.Detail)
		return
	}
	models, err := c.Models(ctx)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, models)
}

// handleAITry runs a watch's AI on one of its recorded changes, so a
// condition can be tried before relying on it.
func (s *Server) handleAITry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := actorID(ctx)
	var in struct {
		ChangeID  int64  `json:"change_id"`
		Condition string `json:"condition"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid json")
		return
	}
	c, err := s.store.GetChange(ctx, uid, in.ChangeID)
	if err != nil {
		s.fail(w, err, "change")
		return
	}
	wa, err := s.store.GetWatch(ctx, uid, c.WatchID)
	if err != nil {
		s.fail(w, err, "watch")
		return
	}
	st, _ := s.store.GetSettings(ctx, uid)
	cl, _, why := s.aiFor(ctx, uid, st)
	if cl == nil {
		writeErr(w, http.StatusServiceUnavailable, why)
		return
	}
	wa.AICondition = in.Condition
	actx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	v, err := s.aiJudge(actx, cl, wa, changedLines(c.Diff, "+"), changedLines(c.Diff, "-"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func validAIProvider(v string) bool  { return core.ValidAIProvider(v) }
func validPocketModel(v string) bool { return core.ValidInferenceModel(v) }
