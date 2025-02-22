package stream

import (
	"fmt"
	"io"

	"github.com/valyala/fastjson"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/fasttime"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/common"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/protoparser/newrelic"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/writeconcurrencylimiter"
)

var parserPool fastjson.ParserPool

// Parse parses NewRelic POST request for newrelic/infra/v2/metrics/events/bulk from reader and calls callback for the parsed request.
//
// callback shouldn't hold series after returning.
func Parse(r io.Reader, isGzip bool, callback func(series []newrelic.Metric) error) error {
	wcr := writeconcurrencylimiter.GetReader(r)
	defer writeconcurrencylimiter.PutReader(wcr)
	r = wcr

	if isGzip {
		zr, err := common.GetGzipReader(r)
		if err != nil {
			return fmt.Errorf("cannot read gzipped Newrelic agent data: %w", err)
		}
		defer common.PutGzipReader(zr)
		r = zr
	}

	ctx := getPushCtx(r)
	defer putPushCtx(ctx)
	if err := ctx.Read(); err != nil {
		return err
	}

	p := parserPool.Get()
	defer parserPool.Put(p)

	v, err := p.ParseBytes(ctx.reqBuf.B)
	if err != nil {
		return fmt.Errorf("cannot parse NewRelic POST request with size %d bytes: %w", len(ctx.reqBuf.B), err)
	}

	metricsPost, err := v.Array()
	if err != nil {
		return fmt.Errorf("cannot fetch data from Newrelic POST request: %w", err)
	}

	var events newrelic.Events

	if err := events.Unmarshal(metricsPost); err != nil {
		unmarshalErrors.Inc()
		return fmt.Errorf("cannot unmarshal NewRelic POST request: %w", err)
	}

	// Fill in missing timestamps
	currentTimestamp := int64(fasttime.UnixTimestamp())
	for i := range events.Metrics {
		m := &events.Metrics[i]
		if m.Timestamp == 0 {
			m.Timestamp = currentTimestamp * 1e3
		}
	}

	if err := callback(events.Metrics); err != nil {
		return fmt.Errorf("error when processing imported data: %w", err)
	}
	return nil
}
