package api

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/nohe-sohbi/mailsorter/backend/internal/models"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

// analysisJobCap bounds how many emails a single async job will process.
const analysisJobCap = 500

func (h *Handler) startAnalysisWorkers(n int) {
	for i := 0; i < n; i++ {
		go h.analysisWorker()
	}
}

func (h *Handler) analysisWorker() {
	for jobID := range h.jobQueue {
		h.processAnalysisJob(jobID)
	}
}

func (h *Handler) processAnalysisJob(jobID string) {
	objectID, err := primitive.ObjectIDFromHex(jobID)
	if err != nil {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	var job models.AnalysisJob
	if err := h.db.AnalysisJobs().FindOne(ctx, bson.M{"_id": objectID}).Decode(&job); err != nil {
		return
	}

	h.updateJob(ctx, objectID, bson.M{"status": "running", "updatedAt": time.Now()})

	onProgress := func(p analysisProgress) {
		h.updateJob(ctx, objectID, bson.M{
			"total":              p.Total,
			"processed":          p.Processed,
			"autoApplied":        p.AutoApplied,
			"suggestionsCreated": p.SuggestionsCreated,
			"cachedHits":         p.CachedHits,
			"updatedAt":          time.Now(),
		})
	}

	p, _, runErr := h.runAnalysis(ctx, job.UserID, job.EmailIDs, onProgress)

	final := bson.M{
		"total":              p.Total,
		"processed":          p.Processed,
		"autoApplied":        p.AutoApplied,
		"suggestionsCreated": p.SuggestionsCreated,
		"cachedHits":         p.CachedHits,
		"updatedAt":          time.Now(),
	}
	if runErr != nil {
		final["status"] = "error"
		final["error"] = runErr.Error()
		log.Printf("analysis job %s failed: %v", jobID, runErr)
	} else {
		final["status"] = "done"
	}
	h.updateJob(ctx, objectID, final)
}

func (h *Handler) updateJob(ctx context.Context, id primitive.ObjectID, set bson.M) {
	h.db.AnalysisJobs().UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": set})
}

// EnqueueAnalyze creates an async analysis job and returns its id immediately,
// so the UI never blocks while hundreds of emails are processed.
func (h *Handler) EnqueueAnalyze(w http.ResponseWriter, r *http.Request) {
	if h.aiClient == nil {
		http.Error(w, "AI service not configured", http.StatusServiceUnavailable)
		return
	}

	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	var req models.AnalyzeEmailsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if len(req.EmailIDs) == 0 {
		http.Error(w, "No email IDs provided", http.StatusBadRequest)
		return
	}
	if len(req.EmailIDs) > analysisJobCap {
		req.EmailIDs = req.EmailIDs[:analysisJobCap]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	job := models.AnalysisJob{
		UserID:    userEmail,
		Status:    "queued",
		Total:     len(req.EmailIDs),
		EmailIDs:  req.EmailIDs,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	res, err := h.db.AnalysisJobs().InsertOne(ctx, job)
	if err != nil {
		http.Error(w, "Failed to create job", http.StatusInternalServerError)
		return
	}
	jobID := res.InsertedID.(primitive.ObjectID).Hex()

	select {
	case h.jobQueue <- jobID:
	default:
		// Queue saturated — run it on its own goroutine so it still completes.
		go h.processAnalysisJob(jobID)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"jobId": jobID, "status": "queued"})
}

// GetJob returns the live status of an analysis job (polled by the client).
func (h *Handler) GetJob(w http.ResponseWriter, r *http.Request) {
	userEmail := r.Header.Get("X-User-Email")
	if userEmail == "" {
		http.Error(w, "User email required", http.StatusUnauthorized)
		return
	}

	objectID, err := primitive.ObjectIDFromHex(mux.Vars(r)["id"])
	if err != nil {
		http.Error(w, "Invalid job ID", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var job models.AnalysisJob
	if err := h.db.AnalysisJobs().FindOne(ctx, bson.M{"_id": objectID, "userId": userEmail}).Decode(&job); err != nil {
		http.Error(w, "Job not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(job)
}
