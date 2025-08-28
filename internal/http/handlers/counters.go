package handlers

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/couchbase/gocb/v2"
	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"

	"github.com/ssinghl/couchbase-go/internal/couchbase"
	"github.com/ssinghl/couchbase-go/pkg/response"
)

// test override hook for counters
var (
	overrideCounter func(bucket, scope, collection, id string, delta int64, initial *uint64, ttlSeconds *uint32) (value uint64, cas uint64, err error)
)

type counterRequest struct {
	Delta      any    `json:"delta"`
	Initial    *any   `json:"initial"`
	TTLSeconds *any   `json:"ttl_seconds"`
	Scope      string `json:"scope"`
	Collection string `json:"collection"`
}

func parseDelta(v any) (int64, error) {
	switch t := v.(type) {
	case float64:
		if t != math.Trunc(t) {
			return 0, fmt.Errorf("invalid delta")
		}
		return int64(t), nil
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return 0, fmt.Errorf("invalid delta")
		}
		iv, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid delta")
		}
		return iv, nil
	default:
		return 0, fmt.Errorf("invalid delta")
	}
}

func parseOptionalInitial(v *any) (*uint64, error) {
	if v == nil {
		return nil, nil
	}
	switch t := (*v).(type) {
	case float64:
		if t != math.Trunc(t) || t < 0 {
			return nil, fmt.Errorf("invalid initial")
		}
		uv := uint64(t)
		return &uv, nil
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return nil, fmt.Errorf("invalid initial")
		}
		u, err := strconv.ParseUint(s, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid initial")
		}
		return &u, nil
	default:
		return nil, fmt.Errorf("invalid initial")
	}
}

func parseOptionalTTLSeconds(v *any) (*uint32, error) {
	if v == nil {
		return nil, nil
	}
	switch t := (*v).(type) {
	case float64:
		if t != math.Trunc(t) || t < 0 || t > 2147483647 {
			return nil, fmt.Errorf("invalid ttl_seconds")
		}
		uv := uint32(t)
		return &uv, nil
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return nil, fmt.Errorf("invalid ttl_seconds")
		}
		u, err := strconv.ParseUint(s, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid ttl_seconds")
		}
		return ptr(uint32(u)), nil
	default:
		return nil, fmt.Errorf("invalid ttl_seconds")
	}
}

func ptr[T any](v T) *T { return &v }

// CounterOp performs atomic counter operations.
// @Summary Atomic counter increment/decrement
// @Description Increment, decrement, or fetch current unsigned integer counter value.
// @Accept json
// @Produce json
// @Param bucket path string true "Bucket name"
// @Param id path string true "Counter key"
// @Param scope query string false "Scope name (default _default)"
// @Param collection query string false "Collection name (default _default)"
// @Param request body counterRequest true "{"delta":int, "initial":uint64, "ttl_seconds":int}"
// @Success 200 {object} map[string]any
// @Header 200 {string} ETag "CAS ETag of the counter (quoted)"
// @Failure 400 {object} map[string]any
// @Failure 404 {object} map[string]any
// @Failure 503 {object} map[string]any
// @Router /buckets/{bucket}/counters/{id} [post]
func CounterOp(cb *couchbase.Client, logger *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		bucket := chi.URLParam(r, "bucket")
		id := chi.URLParam(r, "id")

		var req counterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			response.JSON(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": "invalid JSON body"})
			return
		}
		scope, collection := resolveScopeAndCollection(req.Scope, req.Collection, r)

		if req.Delta == nil {
			response.JSON(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": "delta is required"})
			return
		}
		delta, derr := parseDelta(req.Delta)
		if derr != nil {
			response.JSON(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": derr.Error()})
			return
		}
		initial, ierr := parseOptionalInitial(req.Initial)
		if ierr != nil {
			response.JSON(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": ierr.Error()})
			return
		}
		ttlPtr, terr := parseOptionalTTLSeconds(req.TTLSeconds)
		if terr != nil {
			response.JSON(w, http.StatusBadRequest, map[string]any{"code": "invalid", "message": terr.Error()})
			return
		}

		// override path
		if overrideCounter != nil {
			val, cas, err := overrideCounter(bucket, scope, collection, id, delta, initial, ttlPtr)
			if err != nil {
				status := mapErrorToStatus(err)
				if status == http.StatusNotFound {
					response.JSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "not_found", "message": "document not found"}})
					return
				}
				response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
				return
			}
			setETag(w, gocb.Cas(cas))
			response.JSON(w, http.StatusOK, map[string]any{"id": id, "value": val, "cas": fmt.Sprintf("%d", cas)})
			if logger != nil {
				logger.Info("counter",
					zap.String("op", "counter"),
					zap.String("bucket", bucket),
					zap.String("scope", scope),
					zap.String("collection", collection),
					zap.String("id", id),
					zap.Int64("delta", delta),
					zap.Uint64("initial", func() uint64 {
						if initial != nil {
							return *initial
						}
						return 0
					}()),
					zap.Duration("latency", time.Since(start)),
				)
			}
			return
		}

		coll, code, err := resolveCollectionHTTP(cb, bucket, scope, collection, r.Context())
		if err != nil {
			response.JSON(w, code, map[string]any{"code": "unavailable", "message": err.Error()})
			return
		}

		// delta == 0: return current value without mutation
		if delta == 0 {
			// Try binary get if available via Get with raw transcoder and parse as uint64
			rawRes, gerr := coll.Get(id, &gocb.GetOptions{Context: r.Context(), Transcoder: gocb.NewRawBinaryTranscoder()})
			if gerr != nil {
				status := mapErrorToStatus(gerr)
				if status == http.StatusNotFound {
					if initial == nil {
						response.JSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "not_found", "message": "document not found"}})
						return
					}
					// if initial provided and missing, create without delta
					opts := &gocb.IncrementOptions{Context: r.Context()}
					if ttlPtr != nil {
						opts.Expiry = time.Duration(*ttlPtr) * time.Second
					}
					if initial != nil {
						opts.Initial = int64(*initial)
					}
					incRes, ierr2 := coll.Binary().Increment(id, opts)
					if ierr2 != nil {
						status := mapErrorToStatus(ierr2)
						response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": ierr2.Error()})
						return
					}
					setETag(w, incRes.Cas())
					response.JSON(w, http.StatusOK, map[string]any{"id": id, "value": incRes.Content(), "cas": fmt.Sprintf("%d", uint64(incRes.Cas()))})
					return
				}
				response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": gerr.Error()})
				return
			}
			var buf []byte
			if err := rawRes.Content(&buf); err != nil {
				response.JSON(w, http.StatusServiceUnavailable, map[string]any{"code": http.StatusText(http.StatusServiceUnavailable), "message": err.Error()})
				return
			}
			// Couchbase binary counters are stored as ASCII decimal
			var val uint64
			if len(buf) > 0 {
				if pv, perr := strconv.ParseUint(string(buf), 10, 64); perr == nil {
					val = pv
				}
			}
			setETag(w, rawRes.Cas())
			response.JSON(w, http.StatusOK, map[string]any{"id": id, "value": val, "cas": fmt.Sprintf("%d", uint64(rawRes.Cas()))})
			return
		}

		if delta > 0 {
			opts := &gocb.IncrementOptions{Context: r.Context()}
			opts.Delta = uint64(delta)
			if initial != nil {
				opts.Initial = int64(*initial)
			}
			if ttlPtr != nil {
				opts.Expiry = time.Duration(*ttlPtr) * time.Second
			}
			res, err := coll.Binary().Increment(id, opts)
			if err != nil {
				status := mapErrorToStatus(err)
				if status == http.StatusNotFound && initial == nil {
					response.JSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "not_found", "message": "document not found"}})
					return
				}
				response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
				return
			}
			setETag(w, res.Cas())
			response.JSON(w, http.StatusOK, map[string]any{"id": id, "value": res.Content(), "cas": fmt.Sprintf("%d", uint64(res.Cas()))})
			if logger != nil {
				logger.Info("counter", zap.String("op", "increment"), zap.String("bucket", bucket), zap.String("scope", scope), zap.String("collection", collection), zap.String("id", id), zap.Int64("delta", delta), zap.Duration("latency", time.Since(start)))
			}
			return
		}

		// delta < 0
		opts := &gocb.DecrementOptions{Context: r.Context()}
		d := uint64(-delta)
		opts.Delta = d
		if initial != nil {
			opts.Initial = int64(*initial)
		}
		if ttlPtr != nil {
			opts.Expiry = time.Duration(*ttlPtr) * time.Second
		}
		res, err := coll.Binary().Decrement(id, opts)
		if err != nil {
			status := mapErrorToStatus(err)
			if status == http.StatusNotFound && initial == nil {
				response.JSON(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "not_found", "message": "document not found"}})
				return
			}
			response.JSON(w, status, map[string]any{"code": http.StatusText(status), "message": err.Error()})
			return
		}
		setETag(w, res.Cas())
		response.JSON(w, http.StatusOK, map[string]any{"id": id, "value": res.Content(), "cas": fmt.Sprintf("%d", uint64(res.Cas()))})
		if logger != nil {
			logger.Info("counter", zap.String("op", "decrement"), zap.String("bucket", bucket), zap.String("scope", scope), zap.String("collection", collection), zap.String("id", id), zap.Int64("delta", delta), zap.Duration("latency", time.Since(start)))
		}
	}
}
