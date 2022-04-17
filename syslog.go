package rdns

import (
	"fmt"
	"strings"

	syslog "github.com/RackSec/srslog"
	"github.com/miekg/dns"
	"github.com/sirupsen/logrus"
)

// Syslog forwards every query unmodified and logs the content to syslog
type Syslog struct {
	id       string
	writer   *syslog.Writer
	resolver Resolver
	opt      SyslogOptions
}

var _ Resolver = &Syslog{}

type SyslogOptions struct {
	// "udp", "tcp", "unix". Defaults to "udp"
	Network string

	// Remote address, defaults to local syslog server
	Address string

	// Priority value as per https://pkg.go.dev/log/syslog#Priority
	Priority int

	// Syslog tag
	Tag string

	// Log responses as well
	LogResponse bool
}

// NewSyslog returns a new instance of a Syslog generator.
func NewSyslog(id string, resolver Resolver, opt SyslogOptions) *Syslog {
	writer, err := syslog.Dial(opt.Network, opt.Address, syslog.Priority(opt.Priority), opt.Tag)
	if err != nil {
		// Log any error but don't block if this fails
		logrus.New().WithError(err).Error("failed to initialize syslog")
	}
	return &Syslog{
		id:       id,
		writer:   writer,
		resolver: resolver,
		opt:      opt,
	}
}

// Resolve passes a DNS query through unmodified. Query details are sent via syslog.
func (r *Syslog) Resolve(q *dns.Msg, ci ClientInfo) (*dns.Msg, error) {
	msg := fmt.Sprintf("id=%s type=query client=%s qtype=%s qname=%s", r.id, ci.SourceIP.String(), qType(q), qName(q))
	if _, err := r.writer.Write([]byte(msg)); err != nil {
		logger(r.id, q, ci).WithError(err).Error("failed to send syslog")
	}

	a, err := r.resolver.Resolve(q, ci)
	if err == nil && a != nil && r.opt.LogResponse {
		if a.Rcode == dns.RcodeSuccess {
			for _, rr := range a.Answer {
				s := strings.ReplaceAll(rr.String(), "\t", " ")
				msg = fmt.Sprintf("id=%s type=answer qname=%s answer=%q", r.id, qType(q), s)
				if _, err := r.writer.Write([]byte(msg)); err != nil {
					logger(r.id, q, ci).WithError(err).Error("failed to send syslog")
				}
			}
		} else {
			msg = fmt.Sprintf("id=%s type=answer qname=%s rcode=%s", r.id, qType(q), dns.RcodeToString[a.Rcode])
			if _, err := r.writer.Write([]byte(msg)); err != nil {
				logger(r.id, q, ci).WithError(err).Error("failed to send syslog")
			}
		}
	}
	return a, err
}

func (r *Syslog) String() string {
	return r.id
}
