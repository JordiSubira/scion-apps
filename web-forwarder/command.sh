#!/bin/bash

cmd_help() {
	cmd_version
	echo
	cat <<-_EOF
	Usage:
        $PROGRAM start
	        Span SCION forwarder processes.
	    $PROGRAM stop
	        Terminate SCION forwarder processes.
	_EOF
}

cmd_start(){
    local_addr_list=("192.168.201.40:80" "192.168.201.40:50080")
    endpoint_addr_list=("192.168.201.10:80" "192.168.201.40:50080")

    i=0
    for local_addr in ${local_addr_list[@]}
    do
        echo "SCION forwarder listening at $local_addr; forwarding traffic to endpoint_addr: ${endpoint_addr_list[$i]}" 
        nohup scion-web-forwarder --local-addr $local_addr --endpoint-addr ${endpoint_addr_list[$i]} --acl ./_example_config/acl.json &> /dev/null &
        (( i = i + 1))
    done
}

cmd_stop(){
    echo "Stopping all web-forwarder processes"
    pkill -f "web-forwarder"
}

PROGRAM="${0##*/}"
COMMAND="$1"
shift

case "$COMMAND" in
    start|stop)
        "cmd_$COMMAND" ;;
    *)  cmd_help; exit 1 ;;
esac