package main

import (
	"html/template"

	"github.com/youtube/vitess/go/vt/health"
	"github.com/youtube/vitess/go/vt/servenv"
	_ "github.com/youtube/vitess/go/vt/status"
	"github.com/youtube/vitess/go/vt/tabletmanager"
	"github.com/youtube/vitess/go/vt/tabletserver"
)

var (
	// tabletTemplate contains the style sheet and the tablet itself.
	tabletTemplate = `
<style>
  table {
    width: 100%;
    border-collapse: collapse;
  }
  td, th {
    border: 1px solid #999;
    padding: 0.5rem;
  }
  .time {
    width: 15%;
  }
  .healthy {
    background-color: LightGreen;
  }
  .unhealthy {
    background-color: Salmon;
  }
  .unhappy {
    background-color: Khaki;
  }
</style>
<table width="100%" border="" frame="">
  <tr border="">
    <td width="25%" border="">
      Alias: {{github_com_youtube_vitess_vtctld_tablet .Tablet.AliasString}}<br>
      Keyspace: {{github_com_youtube_vitess_vtctld_keyspace .Tablet.Keyspace}} Shard: {{github_com_youtube_vitess_vtctld_shard .Tablet.Keyspace .Tablet.Shard}}<br>
      Serving graph: {{github_com_youtube_vitess_vtctld_srv_keyspace .Tablet.Alias.Cell .Tablet.Keyspace}} {{github_com_youtube_vitess_vtctld_srv_shard .Tablet.Alias.Cell .Tablet.Keyspace .Tablet.Shard}} {{github_com_youtube_vitess_vtctld_srv_type .Tablet.Alias.Cell .Tablet.Keyspace .Tablet.Shard .Tablet.Type}}<br>
      Replication graph: {{github_com_youtube_vitess_vtctld_replication .Tablet.Alias.Cell .Tablet.Keyspace .Tablet.Shard}}<br>
      {{if .BlacklistedTables}}
        BlacklistedTables: {{range .BlacklistedTables}}{{.}} {{end}}<br>
      {{end}}
      {{if .DisableQueryService}}
        Query Service disabled by TabletControl<br>
      {{end}}
    </td>
    <td width="25%" border="">
      <a href="/schemaz">Schema</a></br>
      <a href="/debug/query_plans">Schema&nbsp;Query&nbsp;Plans</a></br>
      <a href="/debug/query_stats">Schema&nbsp;Query&nbsp;Stats</a></br>
      <a href="/debug/table_stats">Schema&nbsp;Table&nbsp;Stats</a></br>
    </td>
    <td width="25%" border="">
      <a href="/queryz">Query&nbsp;Stats</a></br>
      <a href="/debug/consolidations">Consolidations</a></br>
      <a href="/querylogz">Current&nbsp;Query&nbsp;Log</a></br>
      <a href="/txlogz">Current&nbsp;Transaction&nbsp;Log</a></br>
    </td>
    <td width="25%" border="">
      <a href="/healthz">Health Check</a></br>
      <a href="/debug/health">Query Service Health Check</a></br>
      <a href="/debug/memcache/">Memcache</a></br>
      <a href="/streamqueryz">Current Stream Queries</a></br>
    </td>
  </tr>
</table>
`

	// healthTemplate is just about the tablet health
	healthTemplate = `
<div style="font-size: x-large">Current status: <span style="padding-left: 0.5em; padding-right: 0.5em; padding-bottom: 0.5ex; padding-top: 0.5ex;" class="{{.CurrentClass}}">{{.CurrentHTML}}</span></div>
<p>Polling health information from {{github_com_youtube_vitess_health_html_name}}. ({{.Config}})</p>
<h2>Health History</h2>
<table>
  <tr>
    <th class="time">Time</th>
    <th>Healthcheck Result</th>
  </tr>
  {{range .Records}}
  <tr class="{{.Class}}">
    <td class="time">{{.Time.Format "Jan 2, 2006 at 15:04:05 (MST)"}}</td>
    <td>{{.HTML}}</td>
  </tr>
  {{end}}
</table>
<dl style="font-size: small;">
  <dt><span class="healthy">healthy</span></dt>
  <dd>serving traffic.</dd>

  <dt><span class="unhappy">unhappy</span></dt>
  <dd>will serve traffic only if there are no fully healthy tablets.</dd>

  <dt><span class="unhealthy">unhealthy</span></dt>
  <dd>will not serve traffic.</dd>
</dl>
`

	// binlogTemplate is about the binlog players
	binlogTemplate = `
{{if .Controllers}}
Binlog player state: {{.State}}</br>
<table>
  <tr>
    <th>Index</th>
    <th>SourceShard</th>
    <th>State</th>
    <th>StopPosition</th>
    <th>LastPosition</th>
    <th>SecondsBehindMaster</th>
    <th>Counts</th>
    <th>Rates</th>
    <th>Last Error</th>
  </tr>
  {{range .Controllers}}
    <tr>
      <td>{{.Index}}</td>
      <td>{{.SourceShardAsHTML}}</td>
      <td>{{.State}}
        {{if eq .State "Running"}}
          {{if .SourceTabletAlias}}
            (from {{github_com_youtube_vitess_vtctld_tablet .SourceTabletAlias}})
          {{else}}
            (picking source tablet)
          {{end}}
        {{end}}</td>
      <td>{{if .StopPosition}}{{.StopPosition}}{{end}}</td>
      <td>{{.LastPosition}}</td>
      <td>{{.SecondsBehindMaster}}</td>
      <td>{{range $key, $value := .Counts}}<b>{{$key}}</b>: {{$value}}<br>{{end}}</td>
      <td>{{range $key, $values := .Rates}}<b>{{$key}}</b>: {{range $values}}{{.}} {{end}}<br>{{end}}</td>
      <td>{{.LastError}}</td>
    </tr>
  {{end}}
</table>
{{else}}
No binlog player is running.
{{end}}
`
)

type healthStatus struct {
	Records []interface{}
	Config  template.HTML
}

func (hs *healthStatus) CurrentClass() string {
	if len(hs.Records) > 0 {
		return hs.Records[0].(*tabletmanager.HealthRecord).Class()
	}
	return "unknown"
}

func (hs *healthStatus) CurrentHTML() template.HTML {
	if len(hs.Records) > 0 {
		return hs.Records[0].(*tabletmanager.HealthRecord).HTML()
	}
	return template.HTML("unknown")
}

func healthHTMLName() template.HTML {
	return health.DefaultAggregator.HTMLName()
}

// For use by plugins which wish to avoid racing when registering status page parts.
var onStatusRegistered func()

func addStatusParts(qsc tabletserver.Controller) {
	servenv.AddStatusPart("Tablet", tabletTemplate, func() interface{} {
		return map[string]interface{}{
			"Tablet":              agent.Tablet(),
			"BlacklistedTables":   agent.BlacklistedTables(),
			"DisableQueryService": agent.DisableQueryService(),
		}
	})
	if agent.IsRunningHealthCheck() {
		servenv.AddStatusFuncs(template.FuncMap{
			"github_com_youtube_vitess_health_html_name": healthHTMLName,
		})
		servenv.AddStatusPart("Health", healthTemplate, func() interface{} {
			return &healthStatus{
				Records: agent.History.Records(),
				Config:  tabletmanager.ConfigHTML(),
			}
		})
	}
	qsc.AddStatusPart()
	servenv.AddStatusPart("Binlog Player", binlogTemplate, func() interface{} {
		return agent.BinlogPlayerMap.Status()
	})
	if onStatusRegistered != nil {
		onStatusRegistered()
	}
}
