package httpapi

import (
	"encoding/json"
	"net/http"
	"regexp"

	"github.com/kamadopool/kamado-api/internal/bitcoind"
	"github.com/kamadopool/kamado-api/internal/store"
)

var txidRE = regexp.MustCompile(`^[0-9a-fA-F]{64}$`)

type accelerateReq struct {
	Txid       string  `json:"txid"`
	FeerateVB  float64 `json:"fee_rate_satvb"`
}

type cancelReq struct {
	Txid string `json:"txid"`
}

func (s *Server) accelerate(w http.ResponseWriter, r *http.Request) {
	if s.Acc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "accelerator not available (no store)"})
		return
	}
	var req accelerateReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
		return
	}
	if !txidRE.MatchString(req.Txid) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid txid: must be 64 hex characters"})
		return
	}
	if req.FeerateVB <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "fee_rate_satvb must be positive"})
		return
	}
	if req.FeerateVB > 2000 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "fee_rate_satvb cannot exceed 2000 sat/vB"})
		return
	}

	result, err := s.Acc.Accelerate(r.Context(), req.Txid, req.FeerateVB)
	if err != nil {
		status := http.StatusInternalServerError
		if bitcoind.IsRPCError(err, -5) {
			status = http.StatusNotFound
		}
		writeJSON(w, status, map[string]any{"error": bitcoind.RPCErrorMessage(err)})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) accelerateCancel(w http.ResponseWriter, r *http.Request) {
	if s.Acc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "accelerator not available"})
		return
	}
	var req cancelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid JSON: " + err.Error()})
		return
	}
	if !txidRE.MatchString(req.Txid) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid txid"})
		return
	}
	if err := s.Acc.Cancel(r.Context(), req.Txid); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) accelerateMax(w http.ResponseWriter, r *http.Request) {
	if s.Acc == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "accelerator not available"})
		return
	}
	rate, err := s.Acc.MaxFeerate(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"max_feerate_satvb": rate})
}

func (s *Server) accelerateList(w http.ResponseWriter, r *http.Request) {
	if s.Acc == nil {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	txs, err := s.Acc.List()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
		return
	}
	if txs == nil {
		txs = []store.BoostedTx{}
	}
	writeJSON(w, http.StatusOK, txs)
}
