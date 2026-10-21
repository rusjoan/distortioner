#!/usr/bin/env python3
"""Generates grafana/dashboards/distortioner.json: python3 dashboard.py"""
import json
import os

DS = {"type": "prometheus", "uid": "${datasource}"}
J = 'job="$job"'
I = "{" + J + "}"
N = 'job="$node_job"'
W = "[$interval]"  # window of every rate and increase

panels = []  # top level
_ids = [0]
_pos = {"x": 0, "y": 0, "h": 0}
_into = [panels]  # where new panels go: the top level or a collapsed row


def nid():
    _ids[0] += 1
    return _ids[0]


def place(w, h):
    if _pos["x"] + w > 24:
        _pos["y"] += _pos["h"]
        _pos["x"], _pos["h"] = 0, 0
    g = {"h": h, "w": w, "x": _pos["x"], "y": _pos["y"]}
    _pos["x"] += w
    _pos["h"] = max(_pos["h"], h)
    return g


def row(title, collapsed=False):
    if _pos["x"]:
        _pos["y"] += _pos["h"]
        _pos["x"], _pos["h"] = 0, 0
    r = {"type": "row", "title": title, "id": nid(), "collapsed": collapsed,
         "gridPos": {"h": 1, "w": 24, "x": 0, "y": _pos["y"]}, "panels": []}
    panels.append(r)
    _pos["y"] += 1
    _into[0] = r["panels"] if collapsed else panels


def targets(exprs):
    return [{"refId": chr(65 + i), "datasource": DS, "expr": e, "legendFormat": legend, "range": True}
            for i, (e, legend) in enumerate(exprs)]


def bars(title, exprs, unit="short", w=8, h=8, stack=True, desc="", overrides=None):
    """A time series drawn as bars, legend at the bottom."""
    d = {"type": "timeseries", "title": title, "id": nid(), "datasource": DS, "gridPos": place(w, h),
         "interval": "$interval", "targets": targets(exprs),
         "fieldConfig": {"defaults": {"unit": unit, "min": 0, "custom": {
             "drawStyle": "bars", "barAlignment": 0, "fillOpacity": 80, "lineWidth": 1, "showPoints": "never",
             "stacking": {"mode": "normal" if stack else "none", "group": "A"}}},
             "overrides": overrides or []},
         "options": {"legend": {"displayMode": "list", "placement": "bottom", "showLegend": True},
                     "tooltip": {"mode": "multi", "sort": "desc"}}}
    if desc:
        d["description"] = desc
    _into[0].append(d)


def top(title, expr, legend, w=8, h=8, desc=""):
    """Totals over the selected range, largest first."""
    d = {"type": "bargauge", "title": title, "id": nid(), "datasource": DS, "gridPos": place(w, h),
         "targets": [{"refId": "A", "datasource": DS, "expr": expr, "legendFormat": legend, "instant": True, "range": False}],
         "fieldConfig": {"defaults": {"unit": "short", "min": 0, "decimals": 0, "color": {"mode": "continuous-BlPu"}},
                         "overrides": []},
         "options": {"orientation": "horizontal", "displayMode": "gradient", "showUnfilled": True, "valueMode": "color",
                     "reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
                     "minVizHeight": 10, "maxVizHeight": 22}}
    if desc:
        d["description"] = desc
    _into[0].append(d)


def tile(title, expr, unit="short", w=3, decimals=0, steps=None, desc=""):
    d = {"type": "stat", "title": title, "id": nid(), "datasource": DS, "gridPos": place(w, 4),
         "targets": [{"refId": "A", "datasource": DS, "expr": expr, "instant": True, "range": False}],
         "fieldConfig": {"defaults": {"unit": unit, "decimals": decimals, "color": {"mode": "thresholds"},
                                      "thresholds": {"mode": "absolute",
                                                     "steps": steps or [{"color": "blue", "value": None}]}},
                         "overrides": []},
         "options": {"reduceOptions": {"calcs": ["lastNotNull"], "fields": "", "values": False},
                     "colorMode": "value", "graphMode": "none", "textMode": "value", "justifyMode": "center"}}
    if desc and title == "Result delivered":  # the other names explain themselves; the icon eats narrow titles
        d["description"] = desc
    _into[0].append(d)


def p95(metric, by, extra="", window=W):
    sel = J + ("," + extra if extra else "")
    by = ", ".join(["le"] + ([by] if by else []))
    return f"histogram_quantile(0.95, sum by ({by}) (rate({metric}_bucket{{{sel}}}{window})))"


def inc(metric, by, extra=""):
    sel = J + ("," + extra if extra else "")
    return f"sum by ({by}) (increase({metric}{{{sel}}}{W}))"


def total(metric, by, extra=""):
    sel = J + ("," + extra if extra else "")
    return f"sort_desc(round(sum by ({by}) (increase({metric}{{{sel}}}[$__range]))) > 0)"


VIDEO = 'kind=~"video|video_note|video_file|animation|video_sticker"'

# Tiles: the selected range unless the name says otherwise.
tile("Users 24h", f'max(distortioner_active_users{{{J},window="24h"}})', w=3,
     desc="Distinct users with at least one request in the last 24 hours.")
tile("Users 30d", f'max(distortioner_active_users{{{J},window="30d"}})', w=3,
     desc="Distinct users with at least one request in the last 30 days.")
tile("Requests", f'round(sum(increase(distortioner_requests_total{I}[$__range]))) or vector(0)', w=3,
     desc="All requests in the selected range, turned away ones included.")
tile("Result delivered", f'sum(increase(distortioner_requests_total{{{J},status="ok"}}[$__range])) / '
     f'clamp_min(sum(increase(distortioner_requests_total{{{J},status=~"ok|failed"}}[$__range])), 1)',
     unit="percentunit", decimals=1, w=5,
     steps=[{"color": "red", "value": None}, {"color": "orange", "value": 0.9}, {"color": "green", "value": 0.97}],
     desc="Share of accepted requests that got a result. Requests turned away by limits and rules are not counted: "
          "they are on the Refusals chart.")
tile("In queue", f'sum(distortioner_jobs_queued{I})', w=3,
     steps=[{"color": "green", "value": None}, {"color": "orange", "value": 5}, {"color": "red", "value": 20}],
     desc="Jobs waiting for a worker right now.")
tile("Video p95", p95("distortioner_job_duration_seconds", "", VIDEO, "[$__range]"), unit="s", decimals=1, w=4,
     desc="95% of video results were sent within this time, over the selected range.")
tile("Free", f'min(distortioner_disk_free_bytes{{{J},volume="data"}})', unit="bytes", decimals=1, w=3,
     steps=[{"color": "red", "value": None}, {"color": "orange", "value": 2e9}, {"color": "green", "value": 10e9}],
     desc="Free space on the volume with the bot's data.")

row("Requests")
bars("By media kind", [(inc("distortioner_requests_total", "kind"), "{{kind}}")],
     desc="Requests per interval.")
bars("Active users and chats", [(f'max by (window) (distortioner_active_users{{{J},window=~"24h|30d"}})', "users {{window}}"),
                                (f'max by (window) (distortioner_active_chats{{{J},window=~"24h|30d"}})', "chats {{window}}")],
     stack=False, desc="Distinct users and chats with a request in the last 24 hours and 30 days.")
bars("Refusals by reason", [(inc("distortioner_rejections_total", "reason"), "{{reason}}")],
     desc="Requests turned away per interval: rate limit, full queue, file too big, no rights, maintenance…")

row("Effects")
top("Top effects", total("distortioner_effects_total", "effect"), "{{effect}}",
    desc="Effects applied in the selected range.")
top("How results were asked for", total("distortioner_requests_by_source_total", "source"), "{{source}}",
    desc="command, prompt (the menu), picker, random (🎲), button under a result.")
bars("Commands and buttons", [(inc("distortioner_commands_total", "command"), "/{{command}}"),
                              (inc("distortioner_buttons_total", "action"), "{{action}}")],
     desc="Commands and button taps per interval.")

row("Processing")
bars("Time to result p95", [(p95("distortioner_job_duration_seconds", "kind"), "{{kind}}")], unit="s", stack=False,
     desc="95% of results of this kind were sent within this time.")
bars("Queue", [(f'sum by (pool) (max_over_time(distortioner_jobs_queued{I}{W}))', "waiting {{pool}}"),
               (f'sum by (pool) (max_over_time(distortioner_jobs_running{I}{W}))', "running {{pool}}")],
     desc="Most jobs at once in the interval.")
bars("Failed jobs", [(inc("distortioner_job_outcomes_total", "outcome", 'outcome!~"ok|canceled"'), "{{outcome}}")],
     desc="Jobs without a result per interval: timeout, too long, unsupported, failed, shutdown.")

row("Telegram API")
bars("Calls by method", [(inc("distortioner_telegram_requests_total", "method"), "{{method}}")],
     desc="Bot API calls per interval; file is a download.")
bars("Errors by code", [(inc("distortioner_telegram_requests_total", "code", 'code!="200"'), "{{code}}")],
     desc="429 flood control, 400 bad request, 403 blocked or no rights, error is the network.")
bars("Latency p95", [(p95("distortioner_telegram_request_duration_seconds", "method", 'method!="getUpdates"'), "{{method}}")],
     unit="s", stack=False, desc="getUpdates is a long poll and is left out.")

row("Files and space")
bars("Files p95", [(p95("distortioner_input_bytes", ""), "input size"),
                   (p95("distortioner_output_bytes", ""), "result size"),
                   (p95("distortioner_media_duration_seconds", ""), "video and sound length")],
     unit="bytes", w=12, stack=False,
     overrides=[{"matcher": {"id": "byName", "options": "video and sound length"},
                 "properties": [{"id": "unit", "value": "s"}, {"id": "custom.axisPlacement", "value": "right"}]}],
     desc="95% of files were at most this big or this long.")
bars("Space taken", [(f'max by (dir) (distortioner_dir_bytes{{{J},dir=~"data|failed_inputs|work"}})', "{{dir}}"),
                     (f'max(distortioner_dir_bytes{{{J},dir="bot_api"}})', "bot api server")],
     unit="bytes", w=12,
     desc="data is the database, failed_inputs saved inputs of failed jobs, work temporary files of running jobs, "
          "bot api server the files downloaded by the local Bot API server.")

row("Details", collapsed=True)
bars("Requests by status", [(inc("distortioner_requests_total", "status"), "{{status}}")])
bars("CPU: bot and tools", [(f'sum(rate(process_cpu_seconds_total{I}{W}))', "bot"),
                            (f'sum by (tool) (rate(distortioner_tool_cpu_seconds_total{I}{W}))', "{{tool}}")],
     desc="Cores in use; ffmpeg, ffprobe and ImageMagick are counted when a run ends.")
bars("Goroutines and open files", [(f'max(go_goroutines{I})', "goroutines"), (f'max(process_open_fds{I})', "open files")],
     stack=False)
bars("Host disk I/O", [(f'sum(rate(node_disk_read_bytes_total{{{N}}}{W}))', "read"),
                       (f'sum(rate(node_disk_written_bytes_total{{{N}}}{W}))', "write")], unit="Bps")
bars("ImageMagick slots", [(f'max(max_over_time(distortioner_cpu_slots_in_use{I}{W}))', "in use"),
                           (f'max(distortioner_cpu_slots{I})', "limit")], stack=False)
bars("Stages p95", [(p95("distortioner_stage_duration_seconds", "stage"), "{{stage}}")], unit="s", stack=False,
     desc="download, process, upload.")
bars("Updates by type", [(inc("distortioner_updates_total", "type"), "{{type}}")])

dashboard = {
    "uid": "distortioner", "title": "distortioner", "tags": ["distortioner", "telegram"],
    "timezone": "browser", "schemaVersion": 41, "version": 2, "editable": True, "graphTooltip": 1,
    "refresh": "1m", "time": {"from": "now-24h", "to": "now"},
    "templating": {"list": [
        {"name": "interval", "label": "Interval", "type": "interval", "auto": True, "auto_count": 48, "auto_min": "1m",
         "query": "1m,5m,15m,30m,1h,3h,6h,12h,1d,7d", "refresh": 2, "hide": 0,
         "current": {"text": "auto", "value": "$__auto_interval_interval"},
         "options": [{"text": t, "value": "$__auto_interval_interval" if t == "auto" else t, "selected": t == "auto"}
                     for t in ["auto", "1m", "5m", "15m", "30m", "1h", "3h", "6h", "12h", "1d", "7d"]]},
        {"name": "datasource", "label": "Data source", "type": "datasource", "query": "prometheus",
         "current": {"text": "Prometheus", "value": "prometheus"}, "hide": 0},
        {"name": "job", "label": "Bot job", "type": "query", "datasource": DS,
         "query": {"query": "label_values(distortioner_build_info, job)", "refId": "job"},
         "definition": "label_values(distortioner_build_info, job)", "refresh": 2,
         "current": {"text": "distortioner", "value": "distortioner"}, "hide": 0},
        {"name": "node_job", "label": "Node job", "type": "query", "datasource": DS,
         "query": {"query": "label_values(node_uname_info, job)", "refId": "node"},
         "definition": "label_values(node_uname_info, job)", "refresh": 2,
         "current": {"text": "node", "value": "node"}, "hide": 0},
    ]},
    "annotations": {"list": []},
    "panels": panels,
}

out = os.path.join(os.path.dirname(os.path.abspath(__file__)), "grafana", "dashboards", "distortioner.json")
with open(out, "w") as f:
    json.dump(dashboard, f, indent=2, ensure_ascii=False)
    f.write("\n")
