set -eu
umask 077
stage="$agent_dir/.pending-install"
binary="$agent_dir/iquops-monitoring-agent"
config="$agent_dir/config.json"
service=iquops-monitoring-agent

backup() {
    if [ -e "$1" ]; then cp -p "$1" "$2"; fi
}

restore() {
    if [ -f "$2" ]; then
        cp -p "$2" "$1.iquops-restore"
        mv -f "$1.iquops-restore" "$1"
    else
        rm -f "$1"
    fi
}

case "$action" in
    prepare)
        if [ -e "$stage/prepared" ]; then exit 1; fi
        rm -rf "$stage"
        mkdir -m 700 "$stage"
        ;;
    activate)
        chmod 755 "$stage/new-binary"
        backup "$binary" "$stage/old-binary"
        if systemctl is-active --quiet "$service"; then touch "$stage/active"; fi
        if [ -f "$stage/new-config" ]; then
            backup "$config" "$stage/old-config"
            backup "$unit" "$stage/old-unit"
            if systemctl is-enabled --quiet "$service"; then touch "$stage/enabled"; fi
            touch "$stage/configure"
        fi
        # 在第一次修改正式路径前记录可恢复状态。
        touch "$stage/prepared"
        mv -f "$stage/new-binary" "$binary"
        if [ -f "$stage/configure" ]; then
            mv -f "$stage/new-config" "$config"
            install -m 0644 "$stage/new-unit" "$unit.iquops-new"
            mv -f "$unit.iquops-new" "$unit"
            systemctl daemon-reload
            systemctl enable "$service"
        fi
        systemctl restart "$service"
        property=$(systemctl show -p MainPID "$service")
        pid=${property#MainPID=}
        test "$pid" -gt 0 && test "$binary" -ef "$proc_root/$pid/exe"
        ;;
    recover)
        if [ -e "$stage/prepared" ]; then
            if [ ! -f "$stage/active" ] && systemctl is-active --quiet "$service"; then systemctl stop "$service"; fi
            restore "$binary" "$stage/old-binary"
            if [ -f "$stage/configure" ]; then
                if [ ! -f "$stage/enabled" ] && systemctl is-enabled --quiet "$service"; then systemctl disable "$service"; fi
                restore "$config" "$stage/old-config"
                restore "$unit" "$stage/old-unit"
                rm -f "$unit.iquops-new"
                systemctl daemon-reload
                if [ -f "$stage/enabled" ]; then systemctl enable "$service"; fi
            fi
            if [ -f "$stage/active" ]; then systemctl restart "$service"; fi
        fi
        rm -rf "$stage"
        ;;
    commit)
        rm -rf "$stage"
        ;;
esac
