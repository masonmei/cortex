package queryrange

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/go-kit/log"
	"github.com/stretchr/testify/require"
	"github.com/weaveworks/common/httpgrpc"
	"go.uber.org/atomic"

	"github.com/cortexproject/cortex/pkg/querier/tripperware"
)

func TestRetry(t *testing.T) {
	t.Parallel()
	var try atomic.Int32

	for _, tc := range []struct {
		name    string
		handler tripperware.Handler
		resp    tripperware.Response
		err     error
	}{
		{
			name: "retry failures",
			handler: tripperware.HandlerFunc(func(_ context.Context, req tripperware.Request) (tripperware.Response, error) {
				if try.Inc() == 5 {
					return &PrometheusResponse{Status: "Hello World"}, nil
				}
				return nil, fmt.Errorf("fail")
			}),
			resp: &PrometheusResponse{Status: "Hello World"},
		},
		{
			name: "don't retry 400s",
			handler: tripperware.HandlerFunc(func(_ context.Context, req tripperware.Request) (tripperware.Response, error) {
				return nil, httpgrpc.Errorf(http.StatusBadRequest, "Bad Request")
			}),
			err: httpgrpc.Errorf(http.StatusBadRequest, "Bad Request"),
		},
		{
			name: "retry 500s",
			handler: tripperware.HandlerFunc(func(_ context.Context, req tripperware.Request) (tripperware.Response, error) {
				return nil, httpgrpc.Errorf(http.StatusInternalServerError, "Internal Server Error")
			}),
			err: httpgrpc.Errorf(http.StatusInternalServerError, "Internal Server Error"),
		},
		{
			name: "last error",
			handler: tripperware.HandlerFunc(func(_ context.Context, req tripperware.Request) (tripperware.Response, error) {
				if try.Inc() == 5 {
					return nil, httpgrpc.Errorf(http.StatusBadRequest, "Bad Request")
				}
				return nil, httpgrpc.Errorf(http.StatusInternalServerError, "Internal Server Error")
			}),
			err: httpgrpc.Errorf(http.StatusBadRequest, "Bad Request"),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			//parallel testing causes data race
			try.Store(0)
			h := NewRetryMiddleware(log.NewNopLogger(), 5, nil).Wrap(tc.handler)
			resp, err := h.Do(context.Background(), nil)
			require.Equal(t, tc.err, err)
			require.Equal(t, tc.resp, resp)
		})
	}
}

func Test_RetryMiddlewareCancel(t *testing.T) {
	t.Parallel()
	var try atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewRetryMiddleware(log.NewNopLogger(), 5, nil).Wrap(
		tripperware.HandlerFunc(func(c context.Context, r tripperware.Request) (tripperware.Response, error) {
			try.Inc()
			return nil, ctx.Err()
		}),
	).Do(ctx, nil)
	require.Equal(t, int32(0), try.Load())
	require.Equal(t, ctx.Err(), err)

	ctx, cancel = context.WithCancel(context.Background())
	_, err = NewRetryMiddleware(log.NewNopLogger(), 5, nil).Wrap(
		tripperware.HandlerFunc(func(c context.Context, r tripperware.Request) (tripperware.Response, error) {
			try.Inc()
			cancel()
			return nil, errors.New("failed")
		}),
	).Do(ctx, nil)
	require.Equal(t, int32(1), try.Load())
	require.Equal(t, ctx.Err(), err)
}
