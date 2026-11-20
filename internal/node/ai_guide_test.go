package node

import (
	"os"
	"testing"
)

func TestEmbeddedAIGuideMatchesRepositoryGuide(t *testing.T) {
	raw, err := os.ReadFile("../../AI_GUIDE.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != AIGuideMD {
		t.Fatal("AI_GUIDE.md and internal/node/ai-guide.md differ; copy the repository guide into the embedded file")
	}
}
