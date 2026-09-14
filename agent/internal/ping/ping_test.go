package ping

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"vpsmon/proto"
)

// ----------------------------------------------------------------------

// newTestScheduler 造一个不真的发包的调度器：probe 立刻返回固定延迟，
// 并记录每个任务被探测了几次。
func newTestScheduler(t *testing.T) (*Scheduler, *recorder) {
	t.Helper()

	rec := &recorder{probes: map[int64]int{}}
	s := New(rec.onResult)
	s.probe = rec.probe
	s.jitter = func(time.Duration) time.Duration { return 0 } // 测试里不等那个随机的首次延迟
	return s, rec
}

type recorder struct {
	mu      sync.Mutex
	probes  map[int64]int
	results []Result
	fail    bool
}

func (r *recorder) probe(_ context.Context, task proto.PingTask) (*float64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.probes[task.ID]++
	if r.fail {
		return nil, errors.New("模拟失败")
	}
	ms := 12.3
	return &ms, nil
}

func (r *recorder) onResult(res Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results = append(r.results, res)
}

func (r *recorder) probeCount(taskID int64) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.probes[taskID]
}

func (r *recorder) allResults() []Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Result(nil), r.results...)
}

func task(id int64, name, target, kind string, interval int) proto.PingTask {
	return proto.PingTask{ID: id, Name: name, Target: target, Kind: kind, Interval: interval}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待「%s」超时", what)
}

// ----------------------------------------------------------------------

// TestApplyStartsStopsAndRestarts 覆盖验收第 8 条的「调度对齐」：
// 新增启动、参数变更重启、删除停止，参数没变的不动。
func TestApplyStartsStopsAndRestarts(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, _ := newTestScheduler(t)
	defer s.Stop()

	// 新增两个
	s.Apply(ctx, []proto.PingTask{
		task(1, "电信", "1.1.1.1", "icmp", 60),
		task(2, "联通", "2.2.2.2", "icmp", 60),
	})
	if s.Running() != 2 {
		t.Fatalf("应当跑两个任务，得到 %d", s.Running())
	}

	// 记下当前的 runner，用来判断「有没有被重启」
	s.mu.Lock()
	r1, r2 := s.running[1], s.running[2]
	s.mu.Unlock()

	// 任务 1 改目标（要重启），任务 2 只改名字（不该重启），任务 3 新增
	s.Apply(ctx, []proto.PingTask{
		task(1, "电信", "9.9.9.9", "icmp", 60),
		task(2, "联通-改名", "2.2.2.2", "icmp", 60),
		task(3, "移动", "3.3.3.3", "icmp", 60),
	})
	if s.Running() != 3 {
		t.Fatalf("应当跑三个任务，得到 %d", s.Running())
	}

	s.mu.Lock()
	newR1, newR2 := s.running[1], s.running[2]
	s.mu.Unlock()

	if newR1 == r1 {
		t.Error("任务 1 改了目标，应当被重启")
	}
	if newR2 != r2 {
		t.Error("任务 2 只改了名字，不该重启——重启会丢掉它的计时相位")
	}

	// 删掉任务 2
	s.Apply(ctx, []proto.PingTask{
		task(1, "电信", "9.9.9.9", "icmp", 60),
		task(3, "移动", "3.3.3.3", "icmp", 60),
	})
	if s.Running() != 2 {
		t.Fatalf("删掉一个后应当剩两个，得到 %d", s.Running())
	}
	s.mu.Lock()
	_, stillThere := s.running[2]
	s.mu.Unlock()
	if stillThere {
		t.Error("任务 2 应当已经停掉")
	}

	// 全部清空
	s.Apply(ctx, nil)
	if s.Running() != 0 {
		t.Errorf("清空后不该还有任务在跑，得到 %d", s.Running())
	}
}

// TestApplyIgnoresInvalidTasks 保证一个坏任务不会连累整批下发。
func TestApplyIgnoresInvalidTasks(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, _ := newTestScheduler(t)
	defer s.Stop()

	s.Apply(ctx, []proto.PingTask{
		task(1, "好的", "1.1.1.1", "icmp", 60),
		task(2, "目标为空", "", "icmp", 60),
		task(3, "tcp 缺端口", "example.com", "tcp", 60),
		task(4, "未知类型", "1.1.1.1", "udp", 60),
		task(0, "ID 不合法", "1.1.1.1", "icmp", 60),
		task(5, "好的 tcp", "example.com:443", "tcp", 60),
	})

	if s.Running() != 2 {
		t.Errorf("只有两个合法任务该跑起来，得到 %d", s.Running())
	}
}

// TestProbeRunsAndReportsResult 覆盖「探测成功后上报延迟」。
func TestProbeRunsAndReportsResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, rec := newTestScheduler(t)
	defer s.Stop()

	s.Apply(ctx, []proto.PingTask{task(1, "电信", "1.1.1.1", "icmp", 10)})

	waitFor(t, "第一次上报", func() bool { return len(rec.allResults()) >= 1 })

	results := rec.allResults()
	if len(results) == 0 {
		t.Fatal("探测完应当上报结果")
	}
	r := results[0]
	if r.TaskID != 1 {
		t.Errorf("task_id 应当是 1，得到 %d", r.TaskID)
	}
	if r.LatencyMS == nil || *r.LatencyMS != 12.3 {
		t.Errorf("延迟应当是 12.3，得到 %v", r.LatencyMS)
	}
	if r.TS <= 0 {
		t.Errorf("ts 应当是 Unix 秒，得到 %d", r.TS)
	}
}

// TestProbeFailureReportsLoss 覆盖「探测失败按丢包上报」——这是卡片变红的依据。
func TestProbeFailureReportsLoss(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, rec := newTestScheduler(t)
	defer s.Stop()

	rec.mu.Lock()
	rec.fail = true
	rec.mu.Unlock()

	s.Apply(ctx, []proto.PingTask{task(1, "打不通", "10.255.255.1", "icmp", 10)})
	waitFor(t, "第一次探测", func() bool { return len(rec.allResults()) >= 1 })

	r := rec.allResults()[0]
	if r.LatencyMS != nil {
		t.Errorf("失败应当上报 nil 延迟（丢包），得到 %v", *r.LatencyMS)
	}
}

// TestStopHaltsEverything 保证 Stop 之后不再有新的探测。
func TestStopHaltsEverything(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, rec := newTestScheduler(t)
	s.Apply(ctx, []proto.PingTask{task(1, "电信", "1.1.1.1", "icmp", 10)})
	waitFor(t, "第一次上报", func() bool { return len(rec.allResults()) >= 1 })

	s.Stop()
	if s.Running() != 0 {
		t.Fatalf("Stop 之后不该还有任务，得到 %d", s.Running())
	}

	before := rec.probeCount(1)
	time.Sleep(150 * time.Millisecond)
	if after := rec.probeCount(1); after != before {
		t.Errorf("Stop 之后不该再探测，探测次数从 %d 变成了 %d", before, after)
	}
}

// ----------------------------------------------------------------------

func TestValidate(t *testing.T) {
	tests := []struct {
		name string
		task proto.PingTask
		ok   bool
	}{
		{"icmp IP", task(1, "n", "1.1.1.1", "icmp", 60), true},
		{"icmp 域名", task(1, "n", "example.com", "icmp", 60), true},
		{"kind 为空按 icmp", task(1, "n", "1.1.1.1", "", 60), true},
		{"tcp 带端口", task(1, "n", "example.com:443", "tcp", 60), true},
		{"tcp 不带端口", task(1, "n", "example.com", "tcp", 60), false},
		{"目标为空", task(1, "n", "", "icmp", 60), false},
		{"未知类型", task(1, "n", "1.1.1.1", "udp", 60), false},
		{"ID 为 0", task(0, "n", "1.1.1.1", "icmp", 60), false},
		{"ID 为负", task(-1, "n", "1.1.1.1", "icmp", 60), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validate(tc.task)
			if tc.ok && err != nil {
				t.Errorf("应当合法，却报 %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("应当被判非法")
			}
		})
	}
}

func TestClampInterval(t *testing.T) {
	tests := []struct {
		in   int
		want time.Duration
	}{
		{0, minInterval},
		{5, minInterval},
		{10, 10 * time.Second},
		{60, time.Minute},
		{3600, time.Hour},
		{99999, maxInterval},
		{-1, minInterval},
	}

	for _, tc := range tests {
		if got := clampInterval(tc.in); got != tc.want {
			t.Errorf("clampInterval(%d) = %v，期望 %v", tc.in, got, tc.want)
		}
	}
}

func TestSameTask(t *testing.T) {
	base := task(1, "电信", "1.1.1.1", "icmp", 60)

	if !sameTask(base, task(1, "改了名字", "1.1.1.1", "icmp", 60)) {
		t.Error("只改名字应当算同一个任务——名字只影响面板显示")
	}
	for _, other := range []proto.PingTask{
		task(1, "电信", "9.9.9.9", "icmp", 60),
		task(1, "电信", "1.1.1.1", "tcp", 60),
		task(1, "电信", "1.1.1.1", "icmp", 30),
	} {
		if sameTask(base, other) {
			t.Errorf("执行参数变了应当算不同任务: %+v", other)
		}
	}
}

func TestMsPtr(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want float64
	}{
		{12_340 * time.Microsecond, 12.3},
		{12_350 * time.Microsecond, 12.4}, // 四舍五入
		{time.Millisecond, 1},
		{0, 0},
		{1500 * time.Millisecond, 1500},
	}

	for _, tc := range tests {
		if got := msPtr(tc.in); *got != tc.want {
			t.Errorf("msPtr(%v) = %v，期望 %v", tc.in, *got, tc.want)
		}
	}
}

func TestTCPProbeUsesRealListenerAndHonorsCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := listener.Accept()
		if err == nil {
			c.Close()
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	latency, err := probeTCP(ctx, listener.Addr().String())
	if err != nil || latency == nil || *latency < 0 {
		t.Fatalf("latency=%v err=%v", latency, err)
	}
	<-done
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := probeTCP(canceled, listener.Addr().String()); err == nil {
		t.Fatal("canceled probe succeeded")
	}
}
func TestStopWaitsForInFlightProbe(t *testing.T) {
	s := New(func(Result) { t.Error("reported after cancellation") })
	s.jitter = func(time.Duration) time.Duration { return 0 }
	entered := make(chan struct{})
	exited := make(chan struct{})
	s.probe = func(ctx context.Context, _ proto.PingTask) (*float64, error) {
		close(entered)
		<-ctx.Done()
		defer close(exited)
		return nil, ctx.Err()
	}
	s.Apply(context.Background(), []proto.PingTask{task(1, "n", "localhost", "icmp", 10)})
	<-entered
	s.Stop()
	select {
	case <-exited:
	default:
		t.Fatal("Stop returned before probe exited")
	}
}
