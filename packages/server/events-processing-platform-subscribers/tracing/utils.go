package tracing

import (
	"context"
	"encoding/json"
	"github.com/openline-ai/openline-customer-os/packages/server/customer-os-common-module/tracing"
	"github.com/openline-ai/openline-customer-os/packages/server/events/eventstore"
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
	"github.com/opentracing/opentracing-go/log"
	"google.golang.org/grpc/metadata"
)

const (
	SpanTagTenant      = "tenant"
	SpanTagComponent   = "component"
	SpanTagAggregateId = "aggregateID"
	SpanTagEntityId    = "entity-id"
)

func StartProjectionTracerSpan(ctx context.Context, operationName string, event eventstore.Event) (context.Context, opentracing.Span) {
	textMapCarrierFromMetaData := GetTextMapCarrierFromEvent(event)

	span, err := opentracing.GlobalTracer().Extract(opentracing.TextMap, textMapCarrierFromMetaData)
	if err != nil {
		serverSpan := opentracing.GlobalTracer().StartSpan(operationName)
		ctx = opentracing.ContextWithSpan(ctx, serverSpan)
		return ctx, serverSpan
	}

	serverSpan := opentracing.GlobalTracer().StartSpan(operationName, ext.RPCServerOption(span))
	ctx = opentracing.ContextWithSpan(ctx, serverSpan)
	return ctx, serverSpan
}

func GetTextMapCarrierFromEvent(event eventstore.Event) opentracing.TextMapCarrier {
	metadataMap := make(opentracing.TextMapCarrier)
	err := json.Unmarshal(event.GetMetadata(), &metadataMap)
	if err != nil {
		return metadataMap
	}
	return metadataMap
}

func GetTextMapCarrierFromMetaData(ctx context.Context) opentracing.TextMapCarrier {
	metadataMap := make(opentracing.TextMapCarrier)
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		for key := range md.Copy() {
			metadataMap.Set(key, md.Get(key)[0])
		}
	}
	return metadataMap
}

func TraceErr(span opentracing.Span, err error, fields ...log.Field) {
	tracing.TraceErr(span, err, fields...)
}

func LogObjectAsJson(span opentracing.Span, name string, object any) {
	tracing.LogObjectAsJson(span, name, object)
}

func InjectSpanContextIntoGrpcMetadata(ctx context.Context, span opentracing.Span) context.Context {
	return tracing.InjectSpanContextIntoGrpcMetadata(ctx, span)
}
