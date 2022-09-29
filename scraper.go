package openldap_exporter

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
	"gopkg.in/ldap.v2"
)

const (
	baseDN    = "cn=Monitor"
	opsBaseDN = "cn=Operations,cn=Monitor"

	monitorCounterObject = "monitorCounterObject"
	monitorCounter       = "monitorCounter"

	monitoredObject = "monitoredObject"
	monitoredInfo   = "monitoredInfo"

	monitorOperation   = "monitorOperation"
	monitorOpCompleted = "monitorOpCompleted"

	monitorReplicationFilter = "contextCSN"
	monitorReplication       = "monitorReplication"
)

type query struct {
	baseDN           string
	searchFilter     string
	searchAttr       string
	metric           *prometheus.GaugeVec
	setData          func([]*ldap.Entry, *query)
	RepolicateResult float64 // to save repolicate nodes update time
}

var (
	monitoredObjectGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: "openldap",
			Name:      "monitored_object",
			Help:      help(baseDN, objectClass(monitoredObject), monitoredInfo),
		},
		[]string{"dn"},
	)
	monitorCounterObjectGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: "openldap",
			Name:      "monitor_counter_object",
			Help:      help(baseDN, objectClass(monitorCounterObject), monitorCounter),
		},
		[]string{"dn"},
	)
	monitorOperationGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: "openldap",
			Name:      "monitor_operation",
			Help:      help(opsBaseDN, objectClass(monitorOperation), monitorOpCompleted),
		},
		[]string{"dn"},
	)
	scrapeCounter = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: "openldap",
			Name:      "scrape",
			Help:      "successful vs unsuccessful ldap scrape attempts",
		},
		[]string{"result"},
	)
	monitorReplicationGauge = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: "openldap",
			Name:      "monitor_replication",
			Help:      help(baseDN, monitorReplication),
		},
		[]string{"id", "type"},
	)
	queries = []*query{
		{
			baseDN:       baseDN,
			searchFilter: objectClass(monitoredObject),
			searchAttr:   monitoredInfo,
			metric:       monitoredObjectGauge,
			setData:      setValue,
		}, {
			baseDN:       baseDN,
			searchFilter: objectClass(monitorCounterObject),
			searchAttr:   monitorCounter,
			metric:       monitorCounterObjectGauge,
			setData:      setValue,
		},
		{
			baseDN:       opsBaseDN,
			searchFilter: objectClass(monitorOperation),
			searchAttr:   monitorOpCompleted,
			metric:       monitorOperationGauge,
			setData:      setValue,
		},
		{
			baseDN:       opsBaseDN,
			searchFilter: objectClass(monitorOperation),
			searchAttr:   monitorOpCompleted,
			metric:       monitorOperationGauge,
			setData:      setValue,
		},
	}
)

func init() {
	prometheus.MustRegister(
		monitoredObjectGauge,
		monitorCounterObjectGauge,
		monitorOperationGauge,
		monitorReplicationGauge,
		scrapeCounter,
	)
}

func help(msg ...string) string {
	return strings.Join(msg, " ")
}

func objectClass(name string) string {
	return fmt.Sprintf("(objectClass=%v)", name)
}

func setValue(entries []*ldap.Entry, q *query) {
	for _, entry := range entries {
		val := entry.GetAttributeValue(q.searchAttr)
		if val == "" {
			// not every entry will have this attribute
			continue
		}
		num, err := strconv.ParseFloat(val, 64)
		if err != nil {
			// some of these attributes are not numbers
			continue
		}
		q.metric.WithLabelValues(entry.DN).Set(num)
	}
}

// parse ldap contextCSN column
func parseContextCSN(val string, fields log.Fields) (gt, count float64, sid string, mod float64, err error) {
	valueBuffer := strings.Split(val, "#")

	t, err := time.Parse("20060102150405.999999Z", valueBuffer[0])
	gt = float64(t.Unix())

	if err != nil {
		log.WithFields(fields).WithError(err).Warn("unexpected gt value")
		return
	}

	count, err = strconv.ParseFloat(valueBuffer[1], 64)
	if err != nil {
		log.WithFields(fields).WithError(err).Warn("unexpected count value")
		return
	}
	sid = valueBuffer[2]
	mod, err = strconv.ParseFloat(valueBuffer[3], 64)
	if err != nil {
		log.WithFields(fields).WithError(err).Warn("unexpected mod value")
		return
	}

	return
}

func setReplicationValue(entries []*ldap.Entry, q *query) {
	for _, entry := range entries {
		val := entry.GetAttributeValue(q.searchAttr)
		if val == "" {
			// not every entry will have this attribute
			continue
		}
		fields := log.Fields{
			"filter": q.searchFilter,
			"attr":   q.searchAttr,
			"value":  val,
		}

		gt, count, sid, mod, err := parseContextCSN(val, fields)
		if err != nil {
			continue
		}

		// save repolicate result
		q.RepolicateResult = gt

		q.metric.WithLabelValues(sid, "gt").Set(gt)
		q.metric.WithLabelValues(sid, "count").Set(count)
		q.metric.WithLabelValues(sid, "mod").Set(mod)
	}
}

func setReplicationDelayValue(entries []*ldap.Entry, q *query) {
	for _, entry := range entries {
		val := entry.GetAttributeValue(q.searchAttr)
		if val == "" {
			// not every entry will have this attribute
			continue
		}
		fields := log.Fields{
			"filter": q.searchFilter,
			"attr":   q.searchAttr,
			"value":  val,
		}
		valueBuffer := strings.Split(val, "#")
		gt, err := time.Parse("20060102150405.999999Z", valueBuffer[0])
		if err != nil {
			log.WithFields(fields).WithError(err).Warn("unexpected gt value")
			continue
		}
		sid := valueBuffer[2]

		q.metric.WithLabelValues(sid, "gt").Set(float64(gt.Unix()))
	}
}

type Scraper struct {
	Net                string
	Addr               string
	User               string
	Pass               string
	Tick               time.Duration
	LdapSync           []string
	log                log.FieldLogger
	Sync               []string
	LdapSyncTimeDetal  bool
	LdapSyncMasterAddr string
}

func (s *Scraper) addReplicationQueries() {
	for _, q := range s.Sync {
		queries = append(queries,
			&query{
				baseDN:       q,
				searchFilter: objectClass("*"),
				searchAttr:   monitorReplicationFilter,
				metric:       monitorReplicationGauge,
				setData:      setReplicationValue,
			},
		)
	}
}

func (s *Scraper) Start(ctx context.Context) {
	s.log = log.WithField("component", "scraper")
	s.addReplicationQueries()
	address := fmt.Sprintf("%s://%s", s.Net, s.Addr)
	s.log.WithField("addr", address).Info("starting monitor loop")
	ticker := time.NewTicker(s.Tick)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.runOnce()
		case <-ctx.Done():
			return
		}
	}
}

func (s *Scraper) runOnce() {
	result := "fail"
	if s.scrape() {
		result = "ok"
	}
	scrapeCounter.WithLabelValues(result).Inc()
}

func (s *Scraper) scrape() bool {
	conn, err := ldap.Dial(s.Net, s.Addr)
	if err != nil {
		s.log.WithError(err).Error("dial failed")
		return false
	}
	defer conn.Close()

	if s.User != "" && s.Pass != "" {
		err = conn.Bind(s.User, s.Pass)
		if err != nil {
			s.log.WithError(err).Error("bind failed")
			return false
		}
	}

	ret := true
	for _, q := range queries {
		if err := s.scrapeQuery(conn, q); err != nil {

			s.log.WithError(err).WithField("filter", q.searchFilter).WithField("base_dn", q.baseDN).Warn("query failed")
			ret = false
		}

		// add replicate nodes delay to master metrics
		if q.searchAttr == monitorReplicationFilter {

			if s.LdapSyncTimeDetal {
				sr, err := s.queryMasterContext(q)
				if err != nil {
					s.log.WithError(err).Error("query master context error")
					return false
				}

				if len(sr.Entries) <= 0 {
					s.log.Error("get master context csn error ,result empty")
					return false
				}

				// only handle the first entrie
				entry := sr.Entries[0]
				val := entry.GetAttributeValue(q.searchAttr)
				if val == "" {
					// not every entry will have this attribute
					continue
				}

				valueBuffer := strings.Split(val, "#")
				gt, err := time.Parse("20060102150405.999999Z", valueBuffer[0])
				if err != nil {
					s.log.WithError(err).Error("time parser error,value=%s", valueBuffer[0])
					return false
				}
				sid := valueBuffer[2]

				// add delay to master metric
				q.metric.WithLabelValues(sid, "delay").Set(float64(gt.Unix()) - q.RepolicateResult)
			}

		}

	}

	return ret
}

func (s *Scraper) scrapeQuery(conn *ldap.Conn, q *query) error {
	req := ldap.NewSearchRequest(
		q.baseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		q.searchFilter, []string{q.searchAttr}, nil,
	)
	sr, err := conn.Search(req)
	if err != nil {
		return err
	}
	q.setData(sr.Entries, q)
	return nil
}

func (s *Scraper) queryMasterContext(q *query) (result *ldap.SearchResult, err error) {
	masterConn, err := ldap.Dial(s.Net, s.LdapSyncMasterAddr)
	if err != nil {
		s.log.WithError(err).Error("dial master node failed")
		return
	}

	defer masterConn.Close()

	err = masterConn.Bind(s.User, s.Pass)
	if err != nil {
		s.log.WithError(err).Error("bind master failed")
		return
	}

	req := ldap.NewSearchRequest(
		q.baseDN, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		q.searchFilter, []string{q.searchAttr}, nil,
	)

	return masterConn.Search(req)

}
