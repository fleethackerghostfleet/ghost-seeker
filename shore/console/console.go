// Package console serves the operator API on shore. Policy, pulls and
// overrides are queued for downlink to the edge on its next exchange.
package console

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"connectrpc.com/connect"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/console"
	"github.com/fleethackerghostfleet/ghost-seeker/edge/inference"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1/ghostseekerv1connect"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/httpx"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/ids"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/policy"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/unitauth"
	"github.com/fleethackerghostfleet/ghost-seeker/shore/store"
	"google.golang.org/protobuf/proto"
)

type Service struct {
	ghostseekerv1connect.UnimplementedConsoleServiceHandler
	policyMu sync.Mutex
	NodeID   string
	St       *store.Store
	Clk      clock.Clock
	// Embedder compiles natural-language interest into vectors; optional.
	Embedder  inference.Analyzer
	Signer    *policy.SigningKey
	Operators *unitauth.Interceptor
	// Edge to address downlink to; "" = broadcast to all edges.
	EdgeID string
}

func view(it *store.Item) *pb.ItemView {
	m := it.Manifest
	item := &pb.Item{Id: m.ItemId, Type: m.Type, CapturedAtUnixMs: m.CapturedAtUnixMs, Source: m.Source, Geo: m.Geo,
		Features: &pb.Features{Unknownness: m.Unknownness}}
	if it.Features != nil {
		item.Features = it.Features
	} else if m.TopLabel != "" {
		item.Features.Detections = []*pb.Detection{{Label: m.TopLabel, Confidence: m.TopConf}}
	}
	if it.LabelOverride != "" {
		item.Features.LabelOverride, item.Features.OverrideBy = it.LabelOverride, it.OverrideBy
	}
	v := &pb.ItemView{Item: item, Score: &pb.Score{Priority: m.Priority}, ManifestSent: it.ManifestAt.UnixMilli() > 0}
	for _, f := range it.Forms {
		item.Forms = append(item.Forms, &pb.Form{Kind: f.Kind, Bytes: f.Total, Mime: f.Mime, Ready: f.Complete})
		v.FormStates = append(v.FormStates, &pb.FormState{Kind: f.Kind, Bytes: f.Total, AckedThrough: f.Received, Complete: f.Complete})
	}
	return v
}

func (s *Service) ListItems(ctx context.Context, req *connect.Request[pb.ListItemsRequest]) (*connect.Response[pb.ListItemsResponse], error) {
	off, _ := strconv.Atoi(req.Msg.PageToken)
	items, total, err := s.St.List(ctx, store.ListFilter{Type: req.Msg.Type, UnknownOnly: req.Msg.UnknownOnly, Label: req.Msg.Label,
		Limit: int(req.Msg.Limit), Offset: off})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp := &pb.ListItemsResponse{Total: total}
	for _, it := range items {
		resp.Items = append(resp.Items, view(it))
	}
	if int64(off+len(items)) < total {
		resp.NextPageToken = strconv.Itoa(off + len(items))
	}
	return connect.NewResponse(resp), nil
}

func (s *Service) GetItem(ctx context.Context, req *connect.Request[pb.GetItemRequest]) (*connect.Response[pb.GetItemResponse], error) {
	it, err := s.St.Get(ctx, req.Msg.ItemId)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	return connect.NewResponse(&pb.GetItemResponse{Item: view(it)}), nil
}

func (s *Service) GetPolicy(ctx context.Context, _ *connect.Request[pb.GetPolicyRequest]) (*connect.Response[pb.GetPolicyResponse], error) {
	p, err := s.St.GetPolicy(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&pb.GetPolicyResponse{Policy: p}), nil
}

// SetPolicy compiles free text (if any) into interest vectors, stores the
// policy and queues it for the edge.
func (s *Service) SetPolicy(ctx context.Context, req *connect.Request[pb.SetPolicyRequest]) (*connect.Response[pb.SetPolicyResponse], error) {
	p := req.Msg.Policy
	if p == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("policy required"))
	}
	s.policyMu.Lock()
	defer s.policyMu.Unlock()
	var prev *pb.Policy
	if s.Operators != nil {
		op, err := s.Operators.OperatorFromHeader(req.Header())
		if err != nil {
			log.Printf("console: SetPolicy operator= echelon= result=rejected reason=%q", err)
			return nil, err
		}
		if len(p.Layers) == 0 {
			err := connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("operator auth requires layered policy"))
			log.Printf("console: SetPolicy operator=%s echelon=%s result=rejected reason=%q", op.ID, op.Echelon.String(), err)
			return nil, err
		}
		prev, err = s.St.GetPolicy(ctx)
		if err != nil {
			wrapped := connect.NewError(connect.CodeInternal, err)
			log.Printf("console: SetPolicy operator=%s echelon=%s result=rejected reason=%q", op.ID, op.Echelon.String(), wrapped)
			return nil, wrapped
		}
		if err := authorizeOperatorLayers(p, prev, op); err != nil {
			log.Printf("console: SetPolicy operator=%s echelon=%s result=rejected reason=%q", op.ID, op.Echelon.String(), err)
			return nil, err
		}
		preserveExpiry := prev == nil
		if prev != nil {
			for _, layer := range prev.Layers {
				if layer != nil && layer.Echelon > op.Echelon {
					preserveExpiry = true
					break
				}
			}
		}
		if preserveExpiry {
			p.ExpiresAtUnixMs = prev.GetExpiresAtUnixMs()
		}
		for _, layer := range p.Layers {
			if layer != nil && layer.Echelon == op.Echelon {
				layer.IssuedBy = op.ID
			}
		}
		log.Printf("console: SetPolicy operator=%s echelon=%s result=accepted reason=authorized", op.ID, op.Echelon.String())
	}
	now := s.Clk.Now()
	if p.Id == "" {
		p.Id = fmt.Sprintf("p-%d", now.UnixMilli())
	}
	if p.Version == 0 {
		if s.Operators == nil {
			var err error
			prev, err = s.St.GetPolicy(ctx)
			if err != nil {
				return nil, connect.NewError(connect.CodeInternal, err)
			}
		}
		version := now.UnixMilli()
		if prev != nil && prev.Version+1 > version {
			version = prev.Version + 1
		}
		p.Version = version
	}
	p.IssuedAtUnixMs = now.UnixMilli()
	if len(p.Layers) > 0 {
		p = policy.Flatten(p, s.EdgeID, now)
		if p.Interest != nil {
			p.Interest.PositiveEmbedding = nil
			p.Interest.NegativeEmbedding = nil
		}
	}
	if p.Emcon == pb.EmconMode_EMCON_MODE_UNSPECIFIED {
		p.Emcon = pb.EmconMode_EMCON_MODE_NORMAL
	}
	if p.Interest == nil {
		p.Interest = &pb.Interest{}
	}
	if s.Embedder != nil {
		if err := s.compileInterest(ctx, p.Interest); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	p.Signature = nil
	if s.Signer != nil {
		if err := s.Signer.Sign(p); err != nil {
			return nil, connect.NewError(connect.CodeInternal, err)
		}
	}
	if err := s.St.SetPolicy(ctx, p); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if _, err := s.St.EnqueueDownlink(ctx, s.EdgeID, &pb.Downlink{Msg: &pb.Downlink_Policy{Policy: p}}, now); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&pb.SetPolicyResponse{Policy: p, Conflicts: p.Conflicts}), nil
}

func authorizeOperatorLayers(request, stored *pb.Policy, operator unitauth.Operator) error {
	var storedLayers []*pb.PolicyLayer
	if stored != nil {
		storedLayers = stored.Layers
	}
	matched := make(map[int]bool)
	for _, requestedLayer := range request.Layers {
		if requestedLayer == nil || requestedLayer.Echelon <= operator.Echelon {
			continue
		}
		index := matchingStoredLayer(requestedLayer, storedLayers, matched)
		if index < 0 || !proto.Equal(requestedLayer, storedLayers[index]) {
			return cannotChangeLayer(requestedLayer.Echelon, operator.Echelon)
		}
		matched[index] = true
	}
	for index, storedLayer := range storedLayers {
		if storedLayer != nil && storedLayer.Echelon > operator.Echelon && !matched[index] {
			return cannotChangeLayer(storedLayer.Echelon, operator.Echelon)
		}
	}
	return nil
}

func matchingStoredLayer(requested *pb.PolicyLayer, stored []*pb.PolicyLayer, matched map[int]bool) int {
	var sameEchelon []int
	for index, layer := range stored {
		if layer != nil && layer.Echelon == requested.Echelon {
			sameEchelon = append(sameEchelon, index)
		}
	}
	if len(sameEchelon) == 1 {
		if matched[sameEchelon[0]] {
			return -1
		}
		return sameEchelon[0]
	}
	if len(sameEchelon) > 1 {
		match := -1
		for _, index := range sameEchelon {
			if !matched[index] && stored[index].IssuedBy == requested.IssuedBy {
				if match >= 0 {
					return -1
				}
				match = index
			}
		}
		return match
	}
	return -1
}

func cannotChangeLayer(layer, operator pb.Echelon) error {
	return connect.NewError(connect.CodePermissionDenied,
		fmt.Errorf("cannot change %s layer with %s token", layer.String(), operator.String()))
}

// compileInterest turns boost/suppress label lists into positive/negative
// embeddings so the edge can match by similarity, not just by exact label.
func (s *Service) compileInterest(ctx context.Context, in *pb.Interest) error {
	if len(in.BoostLabels) > 0 && len(in.PositiveEmbedding) == 0 {
		v, err := s.Embedder.EmbedText(ctx, []string{strings.Join(in.BoostLabels, ", ")})
		if err != nil {
			return err
		}
		in.PositiveEmbedding = v[0]
	}
	if len(in.SuppressLabels) > 0 && len(in.NegativeEmbedding) == 0 {
		v, err := s.Embedder.EmbedText(ctx, []string{strings.Join(in.SuppressLabels, ", ")})
		if err != nil {
			return err
		}
		in.NegativeEmbedding = v[0]
	}
	return nil
}

func (s *Service) OverrideLabel(ctx context.Context, req *connect.Request[pb.OverrideLabelRequest]) (*connect.Response[pb.OverrideLabelResponse], error) {
	o := req.Msg.Override
	if o == nil || o.ItemId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("override required"))
	}
	if err := s.St.SetOverride(ctx, o.ItemId, o.Label, o.By); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if _, err := s.St.EnqueueDownlink(ctx, s.EdgeID, &pb.Downlink{Msg: &pb.Downlink_LabelOverride{LabelOverride: o}}, s.Clk.Now()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&pb.OverrideLabelResponse{}), nil
}

func (s *Service) RequestPull(ctx context.Context, req *connect.Request[pb.RequestPullRequest]) (*connect.Response[pb.RequestPullResponse], error) {
	p := req.Msg.Pull
	if p == nil || p.ItemId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("pull required"))
	}
	if req.Msg.Pull.Original && strings.TrimSpace(req.Msg.Pull.RequestedBy) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("original (full-size) pull is an operator override: requestedBy is required"))
	}
	if _, err := s.St.EnqueueDownlink(ctx, s.EdgeID, &pb.Downlink{Msg: &pb.Downlink_Pull{Pull: p}}, s.Clk.Now()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&pb.RequestPullResponse{}), nil
}

func (s *Service) GetStatus(ctx context.Context, _ *connect.Request[pb.GetStatusRequest]) (*connect.Response[pb.GetStatusResponse], error) {
	c, err := s.St.Counts(ctx)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	resp := &pb.GetStatusResponse{NodeId: s.NodeID, Role: "shore", ItemsTotal: c.Items, ItemsManifested: c.Manifested,
		ItemsUnknown: c.Unknown, ItemsComplete: c.AllFormsComplete}
	if p, _ := s.St.GetPolicy(ctx); p != nil {
		resp.PolicyId = p.Id
	}
	edges, err := s.St.Edges(ctx)
	if err == nil {
		for _, e := range edges {
			if s.EdgeID == "" || e.ID == s.EdgeID {
				resp.Heartbeat = e.Heartbeat
				resp.BytesSent = e.BytesReceived
				age := s.Clk.Now().Sub(e.LastSeen)
				resp.Link = &pb.LinkState{Up: age < 60e9, EstimateBps: e.Heartbeat.GetLinkEstimateBps(), LastSuccessUnixMs: e.LastSeen.UnixMilli()}
			}
		}
	}
	return connect.NewResponse(resp), nil
}

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
		kind := console.ParseForm(parts[1])
		it, err := s.St.Get(r.Context(), parts[0])
		if err != nil {
			http.NotFound(w, r)
			return
		}
		f := it.Form(kind)
		if f == nil || !f.Complete {
			http.Error(w, "not yet received", http.StatusConflict)
			return
		}
		data, err := s.St.ReadForm(parts[0], kind)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", httpx.SafeBlobMime(f.Mime))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Disposition", "inline")
		_, _ = w.Write(data)
	})
}
