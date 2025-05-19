package racer

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

var verbose = false

func TestRace(t *testing.T) {
	t.Parallel()
	t.Run("Primary", func(t *testing.T) {
		t.Parallel()
		racePrimary(t)
	})

	t.Run("PrimaryError", func(t *testing.T) {
		t.Parallel()
		racePrimaryError(t)
	})

	t.Run("PrimaryFallbacks", func(t *testing.T) {
		t.Parallel()
		racePrimaryFallbacks(t)
	})

	t.Run("PrimaryFallbacksError", func(t *testing.T) {
		t.Parallel()
		racePrimaryFallbacksError(t)
	})

	t.Run("PrimaryTimeWait", func(t *testing.T) {
		t.Parallel()
		racePrimaryTimeWait(t)
	})

	t.Run("OnClose", func(t *testing.T) {
		t.Parallel()
		raceOnClose(t)
	})

}

func TestRaceStandard(t *testing.T) {
	t.Parallel()
	verbose = true

	t.Run("Standard", func(t *testing.T) {
		t.Parallel()
		records, racer := generateStandardRacer(t, 5)
		defer racer.CloseWait()

		for {
			ret, err := racer.Next()
			if err != nil {
				if verbose {
					t.Log(err)
				}
				break
			}
			_, ok := records.LoadAndDelete(ret)
			assert.True(t, ok, fmt.Sprintf("task %d not found", ret))
			if verbose {
				t.Log("Next >>>", ret)
			}
		}

		records.Range(func(key, value any) bool {
			assert.Fail(t, "not all tasks finished", key)
			return true
		})
	})

	t.Run("NextOnce", func(t *testing.T) {
		t.Parallel()

		records, racer := generateStandardRacer(t, 12)
		v, err := racer.Next()
		assert.Nil(t, err)
		if verbose {
			t.Log("Next >>>", v)
		}
		records.Delete(v)
		racer.CloseWait()

		records.Range(func(key, value any) bool {
			assert.Fail(t, "not all tasks finished", key)
			return true
		})
	})

	t.Run("RaceDialContext", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithTimeout(context.TODO(), time.Second*15)
		defer cancel()

		var domain = "www.bilibili.com"

		ips, err := net.LookupIP(domain)
		assert.Nil(t, err)
		assert.NotEmpty(t, ips)
		t.Logf("domain: %s > resolve %v", domain, ips)

		var (
			dialer   net.Dialer
			realCost sync.Map
		)

		taskBuilder := func(ip net.IP) Task[net.Conn] {
			return func(ctx context.Context) (net.Conn, error) {
				start := time.Now()
				addr := net.TCPAddr{
					IP:   ip,
					Port: 443,
				}

				conn, err := dialer.DialContext(ctx, "tcp", addr.String())
				if err != nil {
					return nil, err
				}
				realCost.Store(conn.RemoteAddr().String(), time.Since(start))
				return conn, nil
			}
		}

		var (
			primary   = taskBuilder(ips[0])
			fallbacks []Task[net.Conn]
		)

		for _, ip := range ips[1:] {
			var ip = ip
			fallbacks = append(fallbacks, taskBuilder(ip))
		}

		racer := New(ctx, primary, fallbacks...).
			WithTimeWait(time.Millisecond * 200).
			WithMaxGoroutines(3).
			WithOnClose(func(c net.Conn) {
				c.Close()
			})

		for {
			start := time.Now()
			conn, err := racer.Next()
			if err != nil {
				break
			}
			realCost, _ := realCost.Load(conn.RemoteAddr().String())
			t.Logf("Next >>> %s - cost %v, real cost %v", conn.RemoteAddr().String(), time.Since(start), realCost)
		}
	})
}

func BenchmarkRace(b *testing.B) {
	b.Run("Primary", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			racePrimary(b)
		}
	})

	b.Run("PrimaryError", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			racePrimaryError(b)
		}
	})

	b.Run("PrimaryFallbacks", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			racePrimaryFallbacks(b)
		}
	})

	b.Run("PrimaryFallbacksError", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			racePrimaryFallbacksError(b)
		}
	})

	b.Run("OnClose", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			raceOnClose(b)
		}
	})

}

func racePrimary(t testing.TB) {
	racer := New(context.Background(), mockTask{Ret: 1}.ToTask())
	defer racer.CloseWait()
	raceValidate(t, racer, []int{1})
}

func racePrimaryError(t testing.TB) {
	racer := New(context.Background(), mockTask{Ret: 1, Err: true}.ToTask())
	defer racer.CloseWait()
	_, err := racer.Next()
	assert.Equal(t, err, ErrTasksFinished)
}

func racePrimaryFallbacks(t testing.TB) {
	var (
		tasks []Task[int]
		rets  []int
		n     = 1 + rand.Intn(100)
	)
	for i := 0; i < n; i++ {
		tasks = append(tasks, mockTask{Ret: i}.ToTask())
		rets = append(rets, i)
	}
	racer := New(context.Background(), tasks[0], tasks[1:]...)
	defer racer.CloseWait()
	raceSortValidate(t, racer, rets)
}

func racePrimaryFallbacksError(t testing.TB) {
	var (
		tasks []Task[int]
		rets  []int
		n     = 1 + rand.Intn(100)
	)
	for i := 0; i < n; i++ {
		success := rand.Int()%2 == 0
		if success {
			tasks = append(tasks, mockTask{Ret: i}.ToTask())
			rets = append(rets, i)
		} else {
			tasks = append(tasks, mockTask{Ret: i, Err: true}.ToTask())
		}
	}
	racer := New(context.Background(), tasks[0], tasks[1:]...)
	defer racer.CloseWait()

	raceSortValidate(t, racer, rets)
}

func racePrimaryTimeWait(t testing.TB) {
	var (
		t1 = time.Millisecond * time.Duration(rand.Intn(1000))
		t2 = time.Millisecond * time.Duration(rand.Intn(1000))
	)
	racer := New(context.Background(), mockTask{Ret: 1, Sleep: t1}.ToTask()).WithTimeWait(t2)
	defer racer.CloseWait()

	raceValidate(t, racer, []int{1})
}

func raceOnClose(t testing.TB) {
	var (
		n = rand.Intn(100) + 2
		m = sync.Map{}
	)

	var tasks []Task[int]
	for i := 0; i < n; i++ {
		var i = i
		tasks = append(tasks, func(ctx context.Context) (int, error) {
			m.Store(i, struct{}{})
			return i, nil
		})
	}

	racer := New(context.TODO(), tasks[0], tasks[1:]...).WithOnClose(func(i int) {
		m.Delete(i)
	})

	v, err := racer.Next()
	assert.Nil(t, err)
	assert.Equal(t, 0, v)
	m.Delete(0)

	remain := rand.Intn(n - 1)
	for i := 0; i < remain; i++ {
		v, err = racer.Next()
		m.Delete(v)
		assert.Nil(t, err)
	}
	racer.CloseWait()

	m.Range(func(key, value any) bool {
		assert.Fail(t, "not all tasks finished", key)
		return true
	})
}

func raceValidate(t testing.TB, racer *Racer[int], result []int) {
	for i, v := range result {
		ret, err := racer.Next()

		assert.Nil(t, err, fmt.Sprintf("expected index %d", i))
		assert.Equal(t, v, ret, fmt.Sprintf("expected index %d", i))
	}
	_, err := racer.Next()
	assert.ErrorIs(t, err, ErrTasksFinished)
}

func raceSortValidate(t testing.TB, racer *Racer[int], result []int) {
	var realRets []int
	for {
		ret, err := racer.Next()
		if err != nil {
			break
		}
		realRets = append(realRets, ret)
	}
	sort.Ints(result)
	sort.Ints(realRets)

	_, err := racer.Next()
	assert.ErrorIs(t, err, ErrTasksFinished)
	assert.Equal(t, realRets, result)
}

type mockTask struct {
	Ret   int
	Err   bool
	Sleep time.Duration
}

func (m mockTask) ToTask() Task[int] {
	return func(ctx context.Context) (n int, err error) {
		n = m.Ret
		if m.Err {
			err = fmt.Errorf("error %d", m.Ret)
		}
		if verbose {
			fmt.Printf("start task [%d] - Sleep %v - Err: %v\n", m.Ret, m.Sleep, m.Err)
		}
		if m.Sleep > 0 {
			time.Sleep(m.Sleep)
		}
		if verbose {
			fmt.Printf("end task [%d] - Sleep %v - Err: %v\n", m.Ret, m.Sleep, m.Err)
		}
		return
	}
}

func generateStandardRacer(t testing.TB, n int) (*sync.Map, *Racer[int]) {
	var (
		records     sync.Map
		taskBuilder = func(i int, sleep time.Duration) Task[int] {
			return func(ctx context.Context) (int, error) {
				if verbose {
					t.Logf("start task [%d] sleep %v", i, sleep)
				}
				timer := time.NewTimer(sleep)
				defer timer.Stop()
				select {
				case <-timer.C:
					records.Store(i, struct{}{})
					if verbose {
						t.Logf("end task [%d] sleep %v", i, sleep)
					}
				case <-ctx.Done():
					if verbose {
						t.Logf("cancel task [%d] sleep %v", i, sleep)
					}
					return 0, fmt.Errorf("task [%d] canceled", i)
				}
				return i, nil
			}
		}
		tasks []Task[int]
	)

	tasks = append(tasks, taskBuilder(0, time.Second))

	for i := 1; i < n+1; i++ {
		tasks = append(tasks, taskBuilder(i, time.Millisecond*100*time.Duration(i)))
	}
	racer := New(context.TODO(), tasks[0], tasks[1:]...).WithTimeWait(time.Millisecond * 200).WithOnClose(func(i int) {
		if verbose {
			t.Logf("on close task [%d]", i)
		}
		records.Delete(i)
	})
	return &records, racer
}
