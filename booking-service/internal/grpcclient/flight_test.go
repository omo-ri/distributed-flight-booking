package grpcclient

import (
	"context"
	"testing"

	"google.golang.org/grpc/metadata"

	"github.com/omo-ri/distributed-flight-booking/booking-service/internal/logctx"
)

// trace_id 贯通的发送端：本服务的 trace_id 必须进 outgoing metadata，
// 否则 flight 侧那条汇总行会自生成一个新的，两边就串不起来。
// 接收端（flight 的 logging 拦截器读 incoming metadata）在 flight 侧各自测——
// 中间那段传输是 grpc-go 的保证，不是本仓库的代码，不需要拉全栈再验一遍。
func TestWithMetadataCarriesTraceID(t *testing.T) {
	c := &FlightClient{apiKey: "secret"}
	ctx := logctx.New(context.Background(), "trace-abc")

	md, ok := metadata.FromOutgoingContext(c.withMetadata(ctx))
	if !ok {
		t.Fatal("outgoing metadata 不存在")
	}
	if got := md.Get(traceIDMetadataKey); len(got) != 1 || got[0] != "trace-abc" {
		t.Errorf("%s = %v, want [trace-abc]", traceIDMetadataKey, got)
	}
	if got := md.Get("x-api-key"); len(got) != 1 || got[0] != "secret" {
		t.Errorf("x-api-key = %v, want [secret]", got)
	}
}

// 没有字段袋时（后台任务直接调 gRPC）不该塞一个空的 trace_id——
// 空值会让接收端以为上游传了值，从而放弃自己生成。
func TestWithMetadataOmitsEmptyTraceID(t *testing.T) {
	c := &FlightClient{apiKey: "secret"}

	md, ok := metadata.FromOutgoingContext(c.withMetadata(context.Background()))
	if !ok {
		t.Fatal("outgoing metadata 不存在")
	}
	if got := md.Get(traceIDMetadataKey); len(got) != 0 {
		t.Errorf("%s 不该出现，得到 %v", traceIDMetadataKey, got)
	}
}
