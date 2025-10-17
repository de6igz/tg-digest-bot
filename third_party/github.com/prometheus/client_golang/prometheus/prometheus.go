package prometheus

type Gauge struct{}

type Counter struct{}

type Histogram struct{}

type GaugeVec struct{}

type CounterVec struct{}

type HistogramVec struct{}

type Registry struct{}

type Registerer interface {
	MustRegister(...interface{})
}

type GaugeOpts struct {
	Name string
	Help string
}

type CounterOpts struct {
	Name string
	Help string
}

type HistogramOpts struct {
	Name    string
	Help    string
	Buckets []float64
}

var DefBuckets = []float64{0.1, 1, 5}

var DefaultRegisterer Registerer = NewRegistry()

func NewGauge(opts GaugeOpts) *Gauge       { return &Gauge{} }
func NewCounter(opts CounterOpts) *Counter { return &Counter{} }
func NewHistogram(opts HistogramOpts) *Histogram {
	return &Histogram{}
}

func NewGaugeVec(opts GaugeOpts, _ []string) *GaugeVec       { return &GaugeVec{} }
func NewCounterVec(opts CounterOpts, _ []string) *CounterVec { return &CounterVec{} }
func NewHistogramVec(opts HistogramOpts, _ []string) *HistogramVec {
	return &HistogramVec{}
}

func (g *Gauge) Set(v float64) {}
func (g *Gauge) Inc()          {}
func (g *Gauge) Dec()          {}

func (c *Counter) Inc()          {}
func (c *Counter) Add(v float64) {}

func (h *Histogram) Observe(v float64) {}

func (v *GaugeVec) WithLabelValues(labels ...string) *Gauge         { return &Gauge{} }
func (v *CounterVec) WithLabelValues(labels ...string) *Counter     { return &Counter{} }
func (v *HistogramVec) WithLabelValues(labels ...string) *Histogram { return &Histogram{} }

func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) MustRegister(collectors ...interface{}) {}
