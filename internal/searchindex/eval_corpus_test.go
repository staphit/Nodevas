package searchindex

// The ranked-search eval: a 54-node Chinese/English novel project and 30
// questions typed as whole sentences, each with the nodes that answer it
// (testdata/search-eval, copied from the rag-eval harness's tide-room corpus).
//
// golden.json is what the FTS5 ranker that shipped before the move to
// PostgreSQL returned for every question, written by TestWriteFTS5Golden while
// SQLite still existed. The PostgreSQL ranker must reproduce it.

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const evalDir = "testdata/search-eval"

type evalQuestion struct {
	ID   string   `json:"id"`
	Cat  string   `json:"cat"`
	Q    string   `json:"q"`
	Gold []string `json:"gold"`
}

type goldenHit struct {
	ID    string  `json:"id"`
	Score float64 `json:"score"`
	Match string  `json:"match"`
}

type goldenAnswer struct {
	ID        string      `json:"id"`
	Ranked    []goldenHit `json:"ranked"`
	FirstGold int         `json:"firstGold"`
}

type evalSummary struct {
	R1  float64 `json:"r1"`
	R3  float64 `json:"r3"`
	R5  float64 `json:"r5"`
	MRR float64 `json:"mrr"`
}

type golden struct {
	Generator string         `json:"generator"`
	Limit     int            `json:"limit"`
	Summary   evalSummary    `json:"summary"`
	Answers   []goldenAnswer `json:"answers"`
}

// goldenDepth is how deep firstGold looks; golden.json keeps the top 10.
const goldenDepth = 50

func loadQuestions(t *testing.T) []evalQuestion {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(evalDir, "questions.json"))
	if err != nil {
		t.Fatal(err)
	}
	var questions []evalQuestion
	if err := json.Unmarshal(data, &questions); err != nil {
		t.Fatal(err)
	}
	return questions
}

// evalProject copies the corpus to a temporary project root.
func evalProject(t *testing.T) string {
	t.Helper()
	source := filepath.Join(evalDir, "tide-room")
	root := t.TempDir()
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(source, path)
		target := filepath.Join(root, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// runEval ranks every question and scores the first answer's position.
func runEval(t *testing.T, m *Manager, root string) ([]goldenAnswer, evalSummary) {
	t.Helper()
	questions := loadQuestions(t)
	var answers []goldenAnswer
	var summary evalSummary
	for _, question := range questions {
		ranked, err := m.RankContext(context.Background(), root, question.Q, goldenDepth)
		if err != nil {
			t.Fatalf("%s: %v", question.ID, err)
		}
		answer := goldenAnswer{ID: question.ID}
		for position, hit := range ranked {
			if position < 10 {
				answer.Ranked = append(answer.Ranked, goldenHit{ID: hit.NodeID, Score: hit.Score, Match: hit.Match})
			}
			if answer.FirstGold == 0 && slices.Contains(question.Gold, hit.NodeID) {
				answer.FirstGold = position + 1
			}
		}
		if rank := answer.FirstGold; rank > 0 {
			summary.MRR += 1 / float64(rank)
			if rank <= 1 {
				summary.R1++
			}
			if rank <= 3 {
				summary.R3++
			}
			if rank <= 5 {
				summary.R5++
			}
		}
		answers = append(answers, answer)
	}
	n := float64(len(questions))
	summary.R1, summary.R3, summary.R5, summary.MRR = summary.R1/n, summary.R3/n, summary.R5/n, summary.MRR/n
	return answers, summary
}

func loadGolden(t *testing.T) golden {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(evalDir, "golden.json"))
	if err != nil {
		t.Fatalf("golden.json: %v", err)
	}
	var out golden
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
