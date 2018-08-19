home=`dirname $0`

nohup $home/../log-pilot -template $home/../template/filebeat.tpl -global-template $home/../template/filebeat_global.tpl -log-level debug  -base / > $home/../logs/start.log &