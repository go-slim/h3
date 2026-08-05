package main

import (
	"html/template"
	"io"
)

var reportTemplate = template.Must(template.New("report").Parse(`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
:root {
  color-scheme: light;
  --bg: #f4f7fb;
  --card: #ffffff;
  --ink: #172033;
  --muted: #667085;
  --line: #e5eaf2;
  --track: #edf1f7;
  --bar: #98a2b3;
  --h3: #2563eb;
  --best: #16a34a;
  --shadow: 0 14px 40px rgba(31, 41, 55, .08);
}
* { box-sizing: border-box; }
body {
  margin: 0;
  background: var(--bg);
  color: var(--ink);
  font: 14px/1.5 ui-sans-serif, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
}
main { width: min(1500px, calc(100% - 32px)); margin: 32px auto 64px; }
header {
  padding: 28px 30px;
  border: 1px solid var(--line);
  border-radius: 18px;
  background: linear-gradient(135deg, #fff 0%, #eef5ff 100%);
  box-shadow: var(--shadow);
}
h1 { margin: 0 0 8px; font-size: clamp(26px, 4vw, 42px); letter-spacing: -.04em; }
.subtitle { margin: 0; color: var(--muted); }
.metadata { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 20px; }
.pill {
  padding: 6px 10px;
  border: 1px solid #d9e2ef;
  border-radius: 999px;
  background: rgba(255,255,255,.8);
  color: #344054;
  font-size: 12px;
}
.legend { display: flex; gap: 18px; margin: 18px 2px 26px; color: var(--muted); }
.legend span::before { content: ""; display: inline-block; width: 10px; height: 10px; margin-right: 6px; border-radius: 3px; background: var(--bar); }
.legend .h3::before { background: var(--h3); }
.legend .best::before { background: var(--best); }
.scenario {
  margin-top: 24px;
  padding: 24px;
  border: 1px solid var(--line);
  border-radius: 18px;
  background: var(--card);
  box-shadow: var(--shadow);
}
.scenario h2 { margin: 0 0 18px; font-size: 23px; }
.scenario h2 small { margin-left: 8px; color: var(--muted); font-size: 12px; font-weight: 500; }
.metrics { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 22px; }
.metric { min-width: 0; }
.metric h3 { margin: 0 0 12px; color: #344054; font-size: 13px; }
.row { display: grid; grid-template-columns: 72px minmax(70px, 1fr) 92px 64px; gap: 8px; align-items: center; min-height: 30px; }
.framework { overflow: hidden; color: #475467; font-weight: 650; text-overflow: ellipsis; white-space: nowrap; }
.framework.h3 { color: var(--h3); }
.track { height: 11px; overflow: hidden; border-radius: 999px; background: var(--track); }
.bar { width: max(2px, var(--width)); height: 100%; border-radius: inherit; background: var(--bar); }
.bar.h3 { background: var(--h3); }
.bar.fastest { background: var(--best); }
.bar.h3.fastest { background: linear-gradient(90deg, var(--h3), var(--best)); }
.value { color: #344054; font-variant-numeric: tabular-nums; text-align: right; white-space: nowrap; }
.relative { color: var(--muted); font-size: 12px; text-align: right; white-space: nowrap; }
.relative.fastest { color: var(--best); font-weight: 700; }
details {
  margin-top: 24px;
  border: 1px solid var(--line);
  border-radius: 14px;
  background: var(--card);
}
summary { padding: 14px 18px; cursor: pointer; color: #344054; font-weight: 650; }
pre { max-height: 420px; margin: 0; overflow: auto; padding: 18px; border-top: 1px solid var(--line); background: #111827; color: #d1d5db; font: 12px/1.55 ui-monospace, SFMono-Regular, Menlo, monospace; }
footer { margin-top: 20px; color: var(--muted); font-size: 12px; text-align: center; }
@media (max-width: 1050px) { .metrics { grid-template-columns: 1fr; } }
@media (max-width: 620px) {
  main { width: min(100% - 18px, 1500px); margin-top: 10px; }
  header, .scenario { padding: 18px; border-radius: 14px; }
  .row { grid-template-columns: 62px minmax(50px, 1fr) 82px; }
  .relative { display: none; }
}
</style>
</head>
<body>
<main>
  <header>
    <h1>{{.Title}}</h1>
    <p class="subtitle">进程内路由分发 · 数值越低越好 · {{.SampleCount}}</p>
    <div class="metadata">
      {{range .Environment}}<span class="pill">{{.Name}}: {{.Value}}</span>{{end}}
      <span class="pill">生成时间: {{.GeneratedAt}}</span>
    </div>
  </header>
  <div class="legend">
    <span class="h3">h3</span>
    <span class="best">当前指标最佳</span>
    <span>其他框架</span>
  </div>
  {{range .Scenarios}}
  <section class="scenario" id="{{.Name}}">
    <h2>{{.Title}} <small>{{.Name}}</small></h2>
    <div class="metrics">
      {{range .Metrics}}
      <div class="metric">
        <h3>{{.Name}}</h3>
        {{range .Rows}}
        <div class="row">
          <div class="framework{{if .H3}} h3{{end}}">{{.Framework}}</div>
          <div class="track"><div class="bar{{if .H3}} h3{{end}}{{if .Fastest}} fastest{{end}}" style="--width: {{printf "%.2f" .Width}}%"></div></div>
          <div class="value">{{.Value}}</div>
          <div class="relative{{if .Fastest}} fastest{{end}}">{{.Relative}}</div>
        </div>
        {{end}}
      </div>
      {{end}}
    </div>
  </section>
  {{end}}
  <details>
    <summary>查看原始 benchmark 输出</summary>
    <pre>{{.Raw}}</pre>
  </details>
  <footer>该报告只比较当前机器上的进程内路由、响应重置和 Handler 开销，不代表完整网络服务吞吐量。</footer>
</main>
</body>
</html>`))

func renderReport(w io.Writer, report reportData) error {
	return reportTemplate.Execute(w, report)
}
