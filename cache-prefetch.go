package rdns

import (
	"errors"
	"github.com/miekg/dns"
	"github.com/sirupsen/logrus"
	"time"
)

type CachePrefetch struct {
	CachePrefetchOptions
	id       string
	resolver Resolver
	metrics  CachePrefetchMetrics
}

var _ Resolver = &CachePrefetch{}

type CachePrefetchOptions struct {
	//// Time of cache record ttl polling for record prefetch
	CacheTTLPollingCheckInterval time.Duration

	MaxNumberOfErrorsBeforeDiscardingPrefetchJob int16
	// Number of hits a record gets before prefetch on a record is started
	RecordQueryHitsMin int64
	// Max number of responses to check in the cache. Defaults to 0 which means no limit. If
	// the limit is reached, the least-recently used entry is removed from the cache.
	//TODO
	//RecordCheckCapacity uint32
	// Cache to check records from
	//CacheResolver Cache
	// Allows control over the order of answer RRs in cached responses. Default is to keep
	// the order if nil.
	// TODO
	//ShuffleAnswerFunc AnswerShuffleFunc
	PrefetchSize int
}

func NewCachePrefetch(id string, resolver Resolver, opt CachePrefetchOptions) *CachePrefetch {
	maxNumberOfErrorsBeforeDiscardingPrefetchJob := int16(5)
	c := &CachePrefetch{
		CachePrefetchOptions: opt,
		id:                   id,
		resolver:             resolver,
		metrics:              NewCachePrefetchMetrics(opt.PrefetchSize, maxNumberOfErrorsBeforeDiscardingPrefetchJob, opt.RecordQueryHitsMin),
	}
	if c.MaxNumberOfErrorsBeforeDiscardingPrefetchJob != maxNumberOfErrorsBeforeDiscardingPrefetchJob {
		c.MaxNumberOfErrorsBeforeDiscardingPrefetchJob = maxNumberOfErrorsBeforeDiscardingPrefetchJob
	}

	if c.CacheTTLPollingCheckInterval == 0 {
		c.CacheTTLPollingCheckInterval = time.Minute * 2
	}
	if c.RecordQueryHitsMin == 0 || c.RecordQueryHitsMin == 1 || c.RecordQueryHitsMin == -1 {
		// Set to hit after one record hit
		// fetch opportunistically
		c.RecordQueryHitsMin = 1
	}
	if c.PrefetchSize == 0 {
		c.PrefetchSize = 1000 // defaults to 1000 should be 1/4th of the cache size
	}
	go c.startCachePrefetchJobs()
	return c
}

func (r *CachePrefetch) Resolve(q *dns.Msg, ci ClientInfo) (*dns.Msg, error) {

	if len(q.Question) < 1 {
		return nil, errors.New("no question in query")
	}
	// While multiple questions in one DNS message is part of the standard,
	// it's not actually supported by servers. If we do get one of those,
	// just pass it through and bypass caching.
	if len(q.Question) > 1 {
		return r.resolver.Resolve(q, ci)
	}

	// Get a response from upstream
	a, err := r.resolver.Resolve(q.Copy(), ci)
	if err != nil || a == nil {
		return nil, err
	}

	r.requestAddPrefetchJob(q)
	// Put the upstream response into the cache and return it. Need to store
	// a copy since other elements might modify the response, like the replacer.
	return a, nil
}

func (r *CachePrefetch) String() string {
	return r.id
}
func (r *CachePrefetch) startCachePrefetchJobs() {
	Log.WithFields(logrus.Fields{"id": r.id}).Trace("starting prefetching job")
	for {
		time.Sleep(r.CacheTTLPollingCheckInterval)
		domainEntriesLength := len(r.metrics.items)
		for index, entry := range r.metrics.items {
			Log.WithFields(logrus.Fields{"index": index, "total": domainEntriesLength}).Trace("prefetch")
			r.startCachePrefetchJob(entry)
		}
	}
}
func (r *CachePrefetch) startCachePrefetchJob(item *CachePrefetchEntry) {
	if (item == nil) || (item.msg == nil) || len(item.msg.Question) < 1 {
		return
	}

	if item.prefetchState == PrefetchStateActive { // only prefetch if status is 1
		qname := qName(item.msg)
		qtype := qType(item.msg)

		Log.WithFields(logrus.Fields{"qname": qname, "qtype": qtype}).Trace("prefetch request started")
		var ci ClientInfo
		a, err := r.Resolve(item.msg, ci)

		if err != nil || a == nil {
			Log.WithError(err).Trace("prefetch error")
			r.metrics.addError(item.msg)
		} else {
			if item.errorCount > 0 {
				// reset error count after a successful request
				r.metrics.resetError(item.msg)
				Log.WithFields(logrus.Fields{"qname": qname, "qtype": qtype}).Trace("query reset error count")
			}
			Log.WithFields(logrus.Fields{"qname": qname, "qtype": qtype}).Debug("query prefetched")
		}
	} else {
		Log.WithFields(logrus.Fields{"prefetchState": item.prefetchState, "key": item.key}).Trace("prefetch request status")
	}
}

func (r *CachePrefetch) requestAddPrefetchJob(q *dns.Msg) {
	if q == nil {
		return
	}
	r.metrics.processQuery(q)
}