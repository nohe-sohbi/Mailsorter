//go:build acceptance

package api

// The shared analysis cache across a boot, on the acceptance harness (a real
// MongoDB): what a verdict is stored with, and which ones the boot deletes.
// Wiring, like wiring_imap_test.go: not in the report.
// `scripts/scenarios.sh -run '^TestAcceptance'` runs it, and so does CI.

import (
	"context"
	"testing"
	"time"

	"github.com/nohe-sohbi/mailsorter/backend/internal/database"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
)

// A boot (EnsureIndexes) deletes, in one request, every cached verdict that
// does not carry the current version, those written before verdicts carried
// one included; a verdict the analysis stores now carries it and stays.
func TestAcceptance_BootDropsStaleAnalysisCache(t *testing.T) {
	app := newTestApp(t, &scenarioResult{theme: "hors rapport"})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cache := app.h.db.AnalysisCache()

	// Written by the prompt that listed the user's labels: no version.
	if _, err := cache.InsertOne(ctx, bson.M{"key": "ancienne-cle", "action": "label", "labelName": "Dossier secret", "createdAt": time.Now()}); err != nil {
		t.Fatalf("storing an old verdict: %v", err)
	}
	if _, err := cache.InsertOne(ctx, bson.M{"key": "autre-version", "version": "v1", "action": "archive", "createdAt": time.Now()}); err != nil {
		t.Fatalf("storing a verdict of another version: %v", err)
	}
	// A verdict stored by the analysis now.
	ids := app.receiveAndSync(incoming{From: "Banque <releves@banque.fr>", Subject: "Votre relevé"})
	app.ai.willSay("archive", 0.95)
	app.analyze(ids...)
	var stored models.AnalysisCacheEntry
	if err := cache.FindOne(ctx, bson.M{"key": analysisCacheKey("Banque <releves@banque.fr>", "Votre relevé")}).Decode(&stored); err != nil {
		t.Fatalf("the analysis cached no verdict: %v", err)
	}
	if stored.Version != database.AnalysisCacheVersion {
		t.Errorf("the cached verdict carries version %q, want %q", stored.Version, database.AnalysisCacheVersion)
	}

	if err := app.h.db.EnsureIndexes(ctx); err != nil {
		t.Fatalf("EnsureIndexes: %v", err)
	}

	for _, key := range []string{"ancienne-cle", "autre-version"} {
		if n, err := cache.CountDocuments(ctx, bson.M{"key": key}); err != nil || n != 0 {
			t.Errorf("after the boot, %d cached verdict(s) %q of another version remain (%v), want none", n, key, err)
		}
	}
	if n, err := cache.CountDocuments(ctx, bson.M{"key": stored.Key}); err != nil || n != 1 {
		t.Errorf("after the boot, the current verdict is there %d time(s) (%v), want once", n, err)
	}
}
