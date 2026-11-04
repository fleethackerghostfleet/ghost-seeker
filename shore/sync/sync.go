// Package sync implements the shore side of SyncService: accept manifest
// entries and chunks, ack them, and hand back pending downlink.
package sync

import (
	"context"
	"encoding/json"
	"log/slog"

	"connectrpc.com/connect"
	pb "github.com/fleethackerghostfleet/ghost-seeker/gen/ghostseeker/v1"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/clock"
	"github.com/fleethackerghostfleet/ghost-seeker/internal/fact"
	"github.com/fleethackerghostfleet/ghost-seeker/shore/store"
)

type Service struct {
	st  *store.Store
	clk clock.Clock
	log *slog.Logger
	// OnArrival is called when a form completes (for metrics / UI push).
	OnArrival func(itemID string, kind pb.FormKind)
}

func New(st *store.Store, clk clock.Clock, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{st: st, clk: clk, log: log}
}

// Exchange is the in-process entry point (used directly by the simulator).
func (s *Service) Exchange(ctx context.Context, req *pb.ExchangeRequest) (*pb.ExchangeResponse, error) {
	now := s.clk.Now()
	resp := &pb.ExchangeResponse{ShoreTimeUnixMs: now.UnixMilli()}
	for _, me := range req.Manifest {
		if err := s.st.UpsertManifest(ctx, req.EdgeId, me, now); err != nil {
			return nil, err
		}
		resp.Acks = append(resp.Acks, &pb.Ack{ItemId: me.ItemId, Manifest: true})
	}
	var bytes int64
	for _, c := range req.Chunks {
		bytes += int64(len(c.Data))
		recv, done, err := s.st.WriteChunk(ctx, c, now)
		if err != nil {
			s.log.Warn("chunk", "item", c.ItemId, "err", err)
			continue
		}
		resp.Acks = append(resp.Acks, &pb.Ack{ItemId: c.ItemId, Form: c.Form, AckedThrough: recv, Complete: done})
		if done {
			if c.Form == pb.FormKind_FORM_KIND_FACT {
				s.ingestFact(ctx, c.ItemId)
			}
			if s.OnArrival != nil {
				s.OnArrival(c.ItemId, c.Form)
			}
		}
	}
	if req.Heartbeat != nil {
		if err := s.st.Heartbeat(ctx, req.EdgeId, req.Heartbeat, bytes, now); err != nil {
			return nil, err
		}
	}
	dl, err := s.st.PendingDownlink(ctx, req.EdgeId, req.DownlinkCursor, 50)
	if err != nil {
		return nil, err
	}
	resp.Downlink = dl
	return resp, nil
}

func (s *Service) ingestFact(ctx context.Context, itemID string) {
	raw, err := s.st.ReadForm(itemID, pb.FormKind_FORM_KIND_FACT)
	if err != nil {
		return
	}
	var f fact.Fact
	if err := json.Unmarshal(raw, &f); err != nil {
		s.log.Warn("bad fact", "item", itemID, "err", err)
		return
	}
	feat := f.ToFeatures()
	label := f.TopLabel()
	if err := s.st.SetFeatures(ctx, itemID, feat, label); err != nil {
		s.log.Warn("set features", "item", itemID, "err", err)
	}
}

// Connect adapter.
type Handler struct{ *Service }

func (h Handler) Exchange(ctx context.Context, req *connect.Request[pb.ExchangeRequest]) (*connect.Response[pb.ExchangeResponse], error) {
	resp, err := h.Service.Exchange(ctx, req.Msg)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(resp), nil
}
