package prometheus

type Gauge struct{}

type Counter struct{}

type Histogram struct{}

type Registry struct{}

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

func NewGauge(opts GaugeOpts) *Gauge       { return &Gauge{} }
func NewCounter(opts CounterOpts) *Counter { return &Counter{} }
func NewHistogram(opts HistogramOpts) *Histogram {
return &Histogram{}
}

func (g *Gauge) Set(v float64)      {}
func (c *Counter) Inc()             {}
func (h *Histogram) Observe(v float64) {}

func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) MustRegister(collectors ...interface{}) {}
