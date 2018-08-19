home=`dirname $0`


nohup $home/../log-pilot -template $home/../template/filebeat.tpl -global-template $home/../template/filebeat_global.tpl -log-level debug  -base / -filebeat-config-path /opt/log/filebeat -filebeat-log-path /opt/log/filebeat/log -filebeat-data-path /var/lib/filebeat/data -filebeat-log-level INFO -filebeat-reload-time 10s > $home/../logs/start.log &