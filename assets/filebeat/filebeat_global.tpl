{{range .configList}}
path.config: {{ .Confpath}}
path.logs: {{ .Logpath}}
path.data: {{ .Datapath}}/data
filebeat.registry_file: {{ .Datapath}}/registry
filebeat.shutdown_timeout: 0
logging.level: {{ .Loglevel}}
logging.metrics.enabled: true

setup.template.name: "filebeat"
setup.template.pattern: "filebeat-*"
filebeat.config:
    prospectors:
        enabled: true
        path: ${path.config}/prospectors.d/*.yml
        reload.enabled: true
        reload.period: {{ .Reloadtime}}

output.kafka:
    enabled: true
    codec.format:
      string: '%{[version]}%{[split]}%{[ldc]}%{[split]}%{[hostgroup]}%{[split]}%{[appid]}%{[split]}%{[ip]}%{[split]}%{[path]}%{[split]}%{[@timestamp]}%{[split]}%{[lid]}%{[split]}%{[message]}'
    hosts: [{{ .Brokerlist}}]
    topic: '%{[topic]}'

{{end}}