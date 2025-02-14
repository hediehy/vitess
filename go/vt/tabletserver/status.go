package tabletserver

import (
	"time"

	"github.com/youtube/vitess/go/vt/servenv"
)

// This file contains the status web page export for tabletserver

var queryserviceStatusTemplate = `
<h2>State: {{.State}}</h2>
<h2>Queryservice History</h2>
<table>
  <tr>
    <th>Time</th>
    <th>Target Tablet Type</th>
    <th>Serving State</th>
  </tr>
  {{range .History}}
  <tr>
    <td>{{.Time.Format "Jan 2, 2006 at 15:04:05 (MST)"}}</td>
    <td>{{.TabletType}}</td>
    <td>{{.ServingState}}</td>
  </tr>
  {{end}}
</table>
<div id="qps_chart">QPS: {{.CurrentQPS}}</div>
<script type="text/javascript" src="https://www.google.com/jsapi"></script>
<script type="text/javascript">

google.load("jquery", "1.4.0");
google.load("visualization", "1", {packages:["corechart"]});

function minutesAgo(d, i) {
  var copy = new Date(d);
  copy.setMinutes(copy.getMinutes() - i);
  return copy
}

function drawQPSChart() {
  var div = $('#qps_chart').height(500).width(900).unwrap()[0]
  var chart = new google.visualization.LineChart(div);

  var options = {
    title: "QPS",
    focusTarget: 'category',
    vAxis: {
      viewWindow: {min: 0},
    }
  };

  // If we're accessing status through a proxy that requires a URL prefix,
  // add the prefix to the vars URL.
  var vars_url = '/debug/vars';
  var pos = window.location.pathname.lastIndexOf('/debug/status');
  if (pos > 0) {
    vars_url = window.location.pathname.substring(0, pos) + vars_url;
  }

  var redraw = function() {
    $.getJSON(vars_url, function(input_data) {
      var now = new Date();
      var qps = input_data.QPS;
      var planTypes = Object.keys(qps);
      if (planTypes.length === 0) {
        planTypes = ["All"];
        qps["All"] = [];
      }

      var data = [["Time"].concat(planTypes)];

      for (var i = 0; i < 15; i++) {
        var datum = [minutesAgo(now, i)];
        for (var j = 0; j < planTypes.length; j++) {
          if (i < qps.All.length) {
            datum.push(+qps[planTypes[j]][i].toFixed(2));
          } else {
            datum.push(0);
          }
        }
        data.push(datum)
      }
      chart.draw(google.visualization.arrayToDataTable(data), options);
    })
  };

  redraw();

  // redraw every 30 seconds.
  window.setInterval(redraw, 30000);
}
google.setOnLoadCallback(drawQPSChart);
</script>

`

type queryserviceStatus struct {
	State      string
	History    []interface{}
	CurrentQPS float64
}

// AddStatusPart registers the status part for the status page.
func (tsv *TabletServer) AddStatusPart() {
	servenv.AddStatusPart("Queryservice", queryserviceStatusTemplate, func() interface{} {
		status := queryserviceStatus{
			State:   tsv.GetState(),
			History: tsv.history.Records(),
		}
		rates := tsv.qe.queryServiceStats.QPSRates.Get()
		if qps, ok := rates["All"]; ok && len(qps) > 0 {
			status.CurrentQPS = qps[0]

		}
		return status
	})
}

type historyRecord struct {
	Time         time.Time
	TabletType   string
	ServingState string
}

// IsDuplicate implements history.Deduplicable
func (r *historyRecord) IsDuplicate(other interface{}) bool {
	rother, ok := other.(*historyRecord)
	if !ok {
		return false
	}
	return r.TabletType == rother.TabletType && r.ServingState == rother.ServingState
}
