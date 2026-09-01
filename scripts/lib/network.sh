#!/usr/bin/env bash
# HomeOS — hostname, mDNS service discovery and NSS wiring.
# shellcheck shell=bash

[[ -n "${_HOMEOS_NETWORK_SH:-}" ]] && return 0
_HOMEOS_NETWORK_SH=1

net::set_hostname() {
    log::step "Hostname"
    local want="$HOMEOS_HOSTNAME" current
    current="$(hostnamectl --static 2>/dev/null || cat /etc/hostname 2>/dev/null)"
    current="${current%%$'\n'*}"

    if [[ "$current" == "$want" ]]; then
        log::skip "hostname already $want"
    else
        run hostnamectl set-hostname "$want" \
            || printf '%s\n' "$want" | write_file /etc/hostname 0644 root:root
        log::ok "hostname set to $want (was ${current:-unset})"
    fi

    # Without a 127.0.1.1 entry, sudo and many daemons stall on reverse lookups.
    if grep -qE "^127\.0\.1\.1[[:space:]]" /etc/hosts; then
        run sed -i -E "s/^127\.0\.1\.1[[:space:]].*/127.0.1.1\t${want} ${want}.local/" /etc/hosts
    else
        backup_file /etc/hosts
        printf '127.0.1.1\t%s %s.local\n' "$want" "$want" >> /etc/hosts
    fi
    log::ok "/etc/hosts updated for $want"
}

# Avahi answers <hostname>.local queries; libnss-mdns lets the box itself
# resolve other .local names. Both are needed for the app aliases to work.
net::configure_avahi() {
    log::step "mDNS (${HOMEOS_HOSTNAME}.local)"

    backup_file /etc/avahi/avahi-daemon.conf
    write_file /etc/avahi/avahi-daemon.conf 0644 root:root <<CONF
# Managed by HomeOS.
[server]
host-name=${HOMEOS_HOSTNAME}
domain-name=local
use-ipv4=yes
use-ipv6=yes
# Publishing on docker bridges would advertise unreachable addresses to the LAN.
deny-interfaces=docker0,homeos0
ratelimit-interval-usec=1000000
ratelimit-burst=1000

[wide-area]
enable-wide-area=yes

[publish]
publish-hinfo=no
publish-workstation=no
publish-addresses=yes
# Required so homeos-core can register per-app <app>.local aliases at runtime.
disable-publishing=no

[reflector]
enable-reflector=no

[rlimits]
rlimit-core=0
rlimit-data=8388608
rlimit-fsize=0
rlimit-nofile=768
rlimit-stack=8388608
rlimit-nproc=3
CONF

    net::advertise_service
    net::configure_nsswitch

    svc_enable_now avahi-daemon.service \
        || log::warn "avahi-daemon did not start; ${HOMEOS_HOSTNAME}.local will not resolve"
}

# Advertised over mDNS-SD so phones, Finder and Windows Explorer surface the
# dashboard without anyone typing a URL.
net::advertise_service() {
    ensure_dir /etc/avahi/services 0755 root:root
    write_file /etc/avahi/services/homeos.service 0644 root:root <<XML
<?xml version="1.0" standalone='no'?>
<!DOCTYPE service-group SYSTEM "avahi-service.dtd">
<!-- Managed by HomeOS. -->
<service-group>
  <name replace-wildcards="yes">HomeOS on %h</name>

  <service>
    <type>_http._tcp</type>
    <port>80</port>
    <txt-record>path=/</txt-record>
    <txt-record>product=HomeOS</txt-record>
    <txt-record>version=${HOMEOS_VERSION}</txt-record>
  </service>

  <service>
    <type>_device-info._tcp</type>
    <port>0</port>
    <txt-record>model=RackMac</txt-record>
  </service>
</service-group>
XML
}

# Order matters: mdns4_minimal must precede dns, and [NOTFOUND=return] stops a
# .local miss from stalling on the upstream resolver.
net::configure_nsswitch() {
    local f=/etc/nsswitch.conf
    [[ -f "$f" ]] || { log::warn "$f missing - skipping NSS wiring"; return 0; }

    if grep -qE '^hosts:.*mdns4_minimal' "$f"; then
        log::skip "nsswitch already resolves mDNS"
        return 0
    fi
    backup_file "$f"
    run sed -i -E 's/^(hosts:[[:space:]]+)(.*)$/\1mdns4_minimal [NOTFOUND=return] \2/' "$f"
    log::ok "nsswitch.conf now resolves .local via mDNS"
}

net::report() {
    local ip; ip="$(primary_ip)"
    log::info "LAN address: ${ip} on $(primary_iface 2>/dev/null || echo '?')"
}
