package store

import "context"

type NodeSubscriberTraffic struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	Up   int64  `json:"up"`
	Down int64  `json:"down"`
}

func (db *DB) NodeSubscriberTraffic(ctx context.Context, serverID int64) ([]NodeSubscriberTraffic, error) {
	if err := db.Proxy().ServerExists(ctx, serverID); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT s.id,s.name,s.kind,COALESCE(t.up_bytes,0),COALESCE(t.down_bytes,0)
 FROM subscribers s LEFT JOIN subscriber_traffic t ON t.subscriber_id=s.id AND t.period_start=s.period_start AND t.server_id=?
 WHERE t.server_id IS NOT NULL OR EXISTS(SELECT 1 FROM subscriber_assignments a JOIN inbounds i ON i.id=a.inbound_id WHERE a.subscriber_id=s.id AND i.server_id=?) ORDER BY s.id`, serverID, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NodeSubscriberTraffic{}
	for rows.Next() {
		var item NodeSubscriberTraffic
		if err = rows.Scan(&item.ID, &item.Name, &item.Kind, &item.Up, &item.Down); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
