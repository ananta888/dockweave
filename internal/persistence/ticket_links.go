package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// TicketLink pairs a primary ticket with its mirror (live sync).
type TicketLink struct {
	PrimaryRef string
	MirrorRef  string
	Base       string // JSON of the last synced canonical fields
	SyncedAt   string
}

// LinkByPrimary / LinkByMirror return the link or ErrNotFound.
func (s *Store) LinkByPrimary(ctx context.Context, ref string) (*TicketLink, error) {
	return s.link(ctx, "primary_ref", ref)
}

func (s *Store) LinkByMirror(ctx context.Context, ref string) (*TicketLink, error) {
	return s.link(ctx, "mirror_ref", ref)
}

func (s *Store) link(ctx context.Context, col, ref string) (*TicketLink, error) {
	var l TicketLink
	err := s.db.QueryRowContext(ctx, `SELECT primary_ref, mirror_ref, base, synced_at FROM ticket_links WHERE `+col+`=?`, ref).
		Scan(&l.PrimaryRef, &l.MirrorRef, &l.Base, &l.SyncedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("ticket link: %w", err)
	}
	return &l, nil
}

// PutTicketLink creates or updates a link; a mirror already linked to
// another primary is ErrConflict (one mirror per ticket).
func (s *Store) PutTicketLink(ctx context.Context, l TicketLink) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO ticket_links (primary_ref, mirror_ref, base, synced_at) VALUES (?,?,?,?)
		ON CONFLICT(primary_ref) DO UPDATE SET mirror_ref=excluded.mirror_ref, base=excluded.base, synced_at=excluded.synced_at`,
		l.PrimaryRef, l.MirrorRef, l.Base, nowString())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return fmt.Errorf("%w: mirror %s already linked", ErrConflict, l.MirrorRef)
		}
		return fmt.Errorf("put ticket link: %w", err)
	}
	return nil
}

// CountTicketLinks returns the number of linked tickets.
func (s *Store) CountTicketLinks(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM ticket_links`).Scan(&n)
	return n, err
}
