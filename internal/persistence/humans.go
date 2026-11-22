package persistence

import (
	"context"
	"fmt"

	"github.com/ananta888/dockweave/internal/model"
)

// TouchHuman records a human seen through the web gateway.
func (s *Store) TouchHuman(ctx context.Context, h model.HumanActor) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO humans (id, name, groups, last_seen) VALUES (?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, groups=excluded.groups, last_seen=excluded.last_seen`,
		h.ID, h.Name, encodeJSON(h.Groups), nowString())
	if err != nil {
		return fmt.Errorf("touch human: %w", err)
	}
	return nil
}

// ListHumans returns the known humans, most recently seen first.
func (s *Store) ListHumans(ctx context.Context) ([]model.KnownHuman, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, name, groups, last_seen FROM humans ORDER BY julianday(last_seen) DESC`)
	if err != nil {
		return nil, fmt.Errorf("list humans: %w", err)
	}
	defer rows.Close()
	var out []model.KnownHuman
	for rows.Next() {
		var h model.KnownHuman
		var groups, seen string
		if err := rows.Scan(&h.ID, &h.Name, &groups, &seen); err != nil {
			return nil, err
		}
		h.Groups = decodeStrings(groups)
		h.LastSeen = parseTime(seen)
		out = append(out, h)
	}
	return out, rows.Err()
}
