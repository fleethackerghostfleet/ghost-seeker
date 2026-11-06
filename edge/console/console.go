// Package console serves the operator API on the edge (ship-side reviewer).
package console

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/ingest"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scheduler"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/scoring"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/store"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1/ghostseekerv1connect"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/ids"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
)

type Service struct {
	ghostseekerv1connect.UnimplementedConsoleServiceHandler
	NodeID string
	St     *store.Store
	Sch    *scheduler.Scheduler
	Clk    clock.Clock
	// Deduped reports captures folded into existing items (ingest.Ingester.Deduped); optional.
	Deduped func() int64
	// Prioritizer is the active scoring backend ("edgeml" | "tomer"), reported in GetStatus.
	Prioritizer string
	// Frames is the edge's recent camera-frame log (ship console stream); optional.
	Frames *ingest.FrameLog
}

func view(r *store.Row) *pb.ItemView {
	v := &pb.ItemView{Item: r.Item, Score: r.Score, ManifestSent: r.ManifestAcked}
	for _, f := range r.Forms {
		v.FormStates = append(v.FormStates, &pb.FormState{Kind: f.Kind, Bytes: f.Bytes, AckedThrough: f.AckedThrough, Complete: f.Complete})
	}
	return v
}

func (s *Service) ListItems(ctx context.Context, req *connect.Request[pb.ListItemsRequest]) (*connect.Response[pb.ListItemsResponse], error) {
	rows, err := s.St.All(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	q := req.Msg
	var filtered []*store.Row
	if err := ValidateLabel(q.Label); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	for _, r := range rows {
		if q.Type != pb.ItemType_ITEM_TYPE_UNSPECIFIED && r.Item.Type != q.Type {
			continue
		}
		if q.UnknownOnly && r.Item.GetFeatures().GetUnknownness() < scoring.DefaultUnknownMin {
			continue
		}
		if q.Label != "" {
			l, _ := scoring.TopLabel(r.Item, 0)
			if !strings.Contains(strings.ToLower(l), strings.ToLower(q.Label)) {
				continue
			}
		}
		filtered = append(filtered, r)
	}
	// Highest priority first (stable, so equal priorities keep store order).
	sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].Score.Priority > filtered[j].Score.Priority })
	off, lim, err := Page(q.PageToken, q.Limit)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	resp := &pb.ListItemsResponse{Total: int64(len(filtered))}
	for i := off; i < len(filtered) && i < off+lim; i++ {
		resp.Items = append(resp.Items, view(filtered[i]))
	}
	if off+lim < len(filtered) {
		resp.NextPageToken = strconv.Itoa(off + lim)
	}
	return connect.NewResponse(resp), nil
}

func (s *Service) GetItem(ctx context.Context, req *connect.Request[pb.GetItemRequest]) (*connect.Response[pb.GetItemResponse], error) {
	r, err := s.St.Get(ctx, req.Msg.ItemId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&pb.GetItemResponse{Item: view(r)}), nil
}

func (s *Service) GetQueue(ctx context.Context, req *connect.Request[pb.GetQueueRequest]) (*connect.Response[pb.GetQueueResponse], error) {
	rows, err := s.St.All(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	plan := s.Sch.PreviewPlan(rows, s.Sch.Link().State().EstimateBps, s.Clk.Now())
	resp := &pb.GetQueueResponse{}
	var tb int64
	for i, e := range plan.Queue {
		tb += e.BytesRemaining
		if i < QueueLimit(req.Msg.Limit) {
			resp.Entries = append(resp.Entries, e)
		}
	}
	resp.TotalItems, resp.TotalBytesRemaining = int64(len(plan.Queue)), tb
	return connect.NewResponse(resp), nil
}

func (s *Service) GetPolicy(context.Context, *connect.Request[pb.GetPolicyRequest]) (*connect.Response[pb.GetPolicyResponse], error) {
	return connect.NewResponse(&pb.GetPolicyResponse{Policy: s.Sch.Policy()}), nil
}

func (s *Service) SetPolicy(ctx context.Context, req *connect.Request[pb.SetPolicyRequest]) (*connect.Response[pb.SetPolicyResponse], error) {
	if err := ValidateInterest(req.Msg.GetPolicy().GetInterest()); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if req.Msg.Policy == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("policy required"))
	}
	var sup, boost int64
	var err error
	if signature := req.Msg.Policy.Signature; signature != nil && len(signature.Ed25519) > 0 {
		sup, boost, err = s.Sch.AcceptPolicy(ctx, req.Msg.Policy, "console")
	} else {
		sup, boost, err = s.Sch.AcceptLocalPolicy(ctx, req.Msg.Policy, "console")
	}
	if err != nil {
		code := connect.CodeInvalidArgument
		switch {
		case errors.Is(err, policy.ErrStaleVersion), errors.Is(err, policy.ErrExpired):
			code = connect.CodeFailedPrecondition
		case errors.Is(err, policy.ErrUnsigned), errors.Is(err, policy.ErrUnknownKey), errors.Is(err, policy.ErrBadSignature), errors.Is(err, policy.ErrHigherEchelon):
			code = connect.CodePermissionDenied
		}
		return nil, connect.NewError(code, err)
	}
	applied := s.Sch.Policy()
	return connect.NewResponse(&pb.SetPolicyResponse{Policy: applied, SuppressedCount: sup, BoostedCount: boost, Conflicts: applied.Conflicts}), nil
}

func (s *Service) PolicyAuditHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		limit := 100
		if value := r.URL.Query().Get("limit"); value != "" {
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 0 {
				http.Error(w, "invalid limit", http.StatusBadRequest)
				return
			}
			if parsed > 1000 {
				parsed = 1000
			}
			if parsed > 0 {
				limit = parsed
			}
		}
		entries, err := s.Sch.PolicyAudit(r.Context(), limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"entries": entries})
	})
}

func (s *Service) OverrideLabel(ctx context.Context, req *connect.Request[pb.OverrideLabelRequest]) (*connect.Response[pb.OverrideLabelResponse], error) {
	if req.Msg.Override == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("override required"))
	}
	if err := s.Sch.Override(ctx, req.Msg.Override); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&pb.OverrideLabelResponse{}), nil
}

func (s *Service) RequestPull(ctx context.Context, req *connect.Request[pb.RequestPullRequest]) (*connect.Response[pb.RequestPullResponse], error) {
	if req.Msg.Pull == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("pull required"))
	}
	if req.Msg.Pull.Original && strings.TrimSpace(req.Msg.Pull.RequestedBy) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("original (full-size) pull is an operator override: requestedBy is required"))
	}
	if err := s.Sch.Pull(ctx, req.Msg.Pull); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&pb.RequestPullResponse{}), nil
}

func (s *Service) GetStatus(ctx context.Context, _ *connect.Request[pb.GetStatusRequest]) (*connect.Response[pb.GetStatusResponse], error) {
	rows, err := s.St.All(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	st := s.Sch.Stats()
	resp := &pb.GetStatusResponse{NodeId: s.NodeID, Role: "edge", Link: s.Sch.Link().State(), PolicyId: s.Sch.Policy().Id,
		BytesSent: st.BytesPayload + st.BytesManifest, ItemsTotal: int64(len(rows))}
	resp.ItemsEvicted, err = s.St.ItemsEvicted(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if s.Deduped != nil {
		resp.ItemsDeduped = s.Deduped()
	}
	resp.Prioritizer = s.Prioritizer
	if resp.Prioritizer == "" {
		resp.Prioritizer = scoring.PrioritizerEdgeml
	}
	resp.MetaOnly = st.MetaOnly
	if !st.MetaOnlySince.IsZero() {
		resp.MetaOnlySinceUnixMs = st.MetaOnlySince.UnixMilli()
	}
	resp.MetaOnlyReason = st.MetaOnlyReason
	var queued, qb int64
	for _, r := range rows {
		if r.ManifestAcked {
			resp.ItemsManifested++
		}
		if r.Item.GetFeatures().GetUnknownness() >= scoring.DefaultUnknownMin && r.Item.GetFeatures().GetLabelOverride() == "" {
			resp.ItemsUnknown++
		}
		done := true
		for i := range r.Forms {
			f := &r.Forms[i]
			if !f.Complete && !scheduler.PullOnly(r, f) {
				done = false
				queued++
				qb += f.Bytes - f.AckedThrough
				break
			}
		}
		if done {
			resp.ItemsComplete++
		}
	}
	resp.Heartbeat = &pb.Heartbeat{SentAtUnixMs: s.Clk.Now().UnixMilli(), QueuedItems: queued, QueuedBytes: qb,
		UnmanifestedItems: resp.ItemsTotal - resp.ItemsManifested, PolicyId: resp.PolicyId, LinkEstimateBps: resp.Link.EstimateBps}
	return connect.NewResponse(resp), nil
}

// BlobHandler serves GET /blob/{item_id}/{form} from local storage.
func (s *Service) BlobHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/blob/"), "/")
		if len(parts) != 2 {
			http.NotFound(w, r)
			return
		}
		if !ids.Valid(parts[0]) {
			http.NotFound(w, r)
			return
		}
		kind := parseForm(parts[1])
		row, err := s.St.Get(r.Context(), parts[0])
		if err != nil {
			http.NotFound(w, r)
			return
		}
		f := row.Form(kind)
		if f == nil {
			http.NotFound(w, r)
			return
		}
		data, err := s.St.ReadFormAll(parts[0], kind)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		httpx.ServeBlob(w, f.Mime, data)
	})
}

// ParseForm accepts "fact", "thumb", "crop", "full" or the enum number.
func parseForm(s string) pb.FormKind {
	switch strings.ToLower(s) {
	case "fact", "1":
		return pb.FormKind_FORM_KIND_FACT
	case "thumb", "2":
		return pb.FormKind_FORM_KIND_THUMB
	case "crop", "3":
		return pb.FormKind_FORM_KIND_CROP
	default:
		return pb.FormKind_FORM_KIND_FULL
	}
}

func ParseForm(s string) pb.FormKind { return parseForm(s) }
