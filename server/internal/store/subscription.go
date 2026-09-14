package store

import (
	"context"
	"time"
)

func (q ProxyQueries) SubscriberByToken(ctx context.Context, token string) (*Subscriber, error) {
	s, err := scanSubscriber(q.DB.QueryRowContext(ctx, `SELECT `+subscriberFields+`,sub_token,uuid,password,ss_user_key FROM subscribers WHERE sub_token=?`, token), true)
	if err != nil {
		return nil, err
	}
	err = q.assignments(ctx, []*Subscriber{s})
	return s, err
}

type SubscriberServerTraffic struct {
	ServerID int64  `json:"server_id"`
	Name     string `json:"name"`
	Up       int64  `json:"up"`
	Down     int64  `json:"down"`
}
type SubscriberDailyTraffic struct {
	Date string `json:"date"`
	Up   int64  `json:"up"`
	Down int64  `json:"down"`
}
type SubscriberTraffic struct {
	ByServer []SubscriberServerTraffic `json:"by_server"`
	Daily    []SubscriberDailyTraffic  `json:"daily"`
}

func (db *DB) SubscriberTraffic(ctx context.Context, id int64, now time.Time) (*SubscriberTraffic, error) {
	subscriber, err := db.Proxy().Subscriber(ctx, id)
	if err != nil {
		return nil, err
	}
	out := &SubscriberTraffic{ByServer: []SubscriberServerTraffic{}, Daily: []SubscriberDailyTraffic{}}
	rows, err := db.QueryContext(ctx, `SELECT t.server_id,COALESCE(s.name,'已删除节点'),t.up_bytes,t.down_bytes FROM subscriber_traffic t LEFT JOIN servers s ON s.id=t.server_id WHERE subscriber_id=? AND period_start=? ORDER BY t.server_id`, id, subscriber.PeriodStart)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var v SubscriberServerTraffic
		if err = rows.Scan(&v.ServerID, &v.Name, &v.Up, &v.Down); err != nil {
			rows.Close()
			return nil, err
		}
		out.ByServer = append(out.ByServer, v)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	start := now.AddDate(0, 0, -29).Format(time.DateOnly)
	end := now.Format(time.DateOnly)
	rows, err = db.QueryContext(ctx, `SELECT date,up_bytes,down_bytes FROM subscriber_traffic_daily WHERE subscriber_id=? AND date>=? AND date<=? ORDER BY date`, id, start, end)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	days := map[string]SubscriberDailyTraffic{}
	for rows.Next() {
		var v SubscriberDailyTraffic
		if err = rows.Scan(&v.Date, &v.Up, &v.Down); err != nil {
			return nil, err
		}
		days[v.Date] = v
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	for i := 29; i >= 0; i-- {
		date := now.AddDate(0, 0, -i).Format(time.DateOnly)
		v := days[date]
		v.Date = date
		out.Daily = append(out.Daily, v)
	}
	return out, nil
}
