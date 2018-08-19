{{range .configList}}
- type: log
  enabled: true
  paths:
      - {{ .HostDir }}/{{ .File }}
  scan_frequency: 10s
  fields_under_root: true
  {{if .Stdout}}
  docker-json: true
  {{end}}
  {{if eq .Format "json"}}
  json.keys_under_root: true
  {{end}}
  fields:
      {{range $key, $value := .Tags}}
      {{ $key }}: {{ $value }}
      {{end}}
      {{range $key, $value := $.container}}
      {{ $key }}: {{ $value }}
      {{end}}
      prefix: {{ .Prefix}}
      split: "{{ .Split}}"
      path: {{ .Path}}
      lid: {{ .Lid}}
  tail_files: false
  close_inactive: 2h
  close_eof: false
  close_removed: true
  clean_removed: true
  close_renamed: false

  {{if .Merge}}
  multiline.pattern: {{ .Merge}}
  multiline.negate: false
  multiline.match: before
  multiline.max_lines: 500
  multiline.timeout: 5
  {{end}}

{{end}}