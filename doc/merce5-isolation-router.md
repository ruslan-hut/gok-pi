# Merce 5: isolation router for on-site battery diagnosis

The three Sonnen batteries at Merce 5 went offline after the site MikroTik
(`192.168.8.0/24`, see [pi-agent-setup.md](pi-agent-setup.md#migrating-an-existing-agent-merce-5))
was removed. The batteries now sit in the site network `192.168.88.0/24`, and the
suspicion is an IP address conflict there.

Plan: unplug the batteries from the site network, connect them and a laptop to a
spare MikroTik that knows nothing about the site, find out which address each
battery has, check for duplicates, and open the batteries' web UI.

## The router

| | |
|---|---|
| Model | hAP ac lite, RB952Ui-5ac2nD, serial D3D50D1B9C9A |
| RouterOS | 7.12.1 (RouterBOOT 6.45.9, not upgraded) |
| Identity | `iso-mikrotik` |
| Address | **192.168.88.250/24** on `bridge`; the default `.1` is removed |
| LAN ports | ether2–ether5 (bridge) |
| ether1 | default WAN (DHCP client); leave it **unplugged** |
| Wi‑Fi | `iso-mikrotik` (2.4 GHz) and `iso-mikrotik-5g` (5 GHz), WPA2-PSK AES, WPS off, bridged with the LAN ports; key held by the owner (not stored here) |
| DHCP server | on, pool `192.168.88.200–239`, lease 10 min, **no gateway, no DNS** |
| Services | ssh 22, winbox 8291, www 80; telnet, ftp, api, api-ssl disabled |
| Login | `admin`, password set by the owner (not stored here) |
| SSH | password login allowed (`always-allow-password-login=yes`); key of `ruslanhut@PocoMac.local` imported |

Why it is set up this way:

- `.250`, not `.1`: if the router ends up plugged into the site network by
  mistake it must not pose as the site gateway.
- DHCP stays on, but outside the usual range: batteries that use DHCP show up in
  the lease table with their MAC and hostname. The router has no uplink, so it
  hands out no gateway and no DNS — otherwise the laptop routes its internet
  through the cable and loses Wi‑Fi/phone internet.
- A password user with an SSH key is refused password login by default in
  RouterOS 7; `always-allow-password-login=yes` lets another laptop log in
  with the password.

Applied to a factory-reset router:

```
/interface wireless security-profiles add name=iso mode=dynamic-keys authentication-types=wpa2-psk unicast-ciphers=aes-ccm group-ciphers=aes-ccm wpa2-pre-shared-key=<key>
/interface wireless set wlan1 ssid=iso-mikrotik    band=2ghz-b/g/n  security-profile=iso wps-mode=disabled country=spain disabled=no
/interface wireless set wlan2 ssid=iso-mikrotik-5g band=5ghz-a/n/ac security-profile=iso wps-mode=disabled country=spain disabled=no
/ip address add address=192.168.88.250/24 interface=bridge comment=isolation
/ip address remove [find address="192.168.88.1/24"]
/ip pool set default-dhcp ranges=192.168.88.200-192.168.88.239
/ip dhcp-server network set [find address="192.168.88.0/24"] gateway="" dns-server=""
/ip dns static set [find name=router.lan] address=192.168.88.250
/ip service disable telnet,ftp,api,api-ssl
/ip ssh set always-allow-password-login=yes
/system identity set name=iso-mikrotik
/password
```

Factory reset if ever needed: power off, hold RESET, power on, release when the
USR/ACT LED starts flashing (~5 s). Holding longer enters CAPs (~10 s) or
Netinstall (15 s+) mode. After reset: `192.168.88.1`, user `admin`, empty password.

## On site

### Connect

1. Laptop: internet over Wi‑Fi or the phone, cable to **ether2–5** of the
   router. The laptop gets `192.168.88.200–239` and keeps its internet.
   Without a cable: join Wi‑Fi `iso-mikrotik`. The laptop's only Wi‑Fi is then
   taken, so internet has to come from the phone over USB or Bluetooth
   tethering.
2. Check: `ping 192.168.88.250`, then `ssh admin@192.168.88.250`.
3. Before unplugging the batteries, note which port of the site switch/router
   each one uses, so they can go back.
4. Batteries: unplug from the site network, plug into the router's ether2–5
   (one at a time if there are not enough ports, or through a small switch).

### Find the batteries

On the router:

```
/ip dhcp-server lease print          # batteries on DHCP: address, MAC, hostname
/ip arp print                        # everything the router has heard from
/tool ip-scan interface=bridge address-range=192.168.88.0/24 duration=30s
/tool ip-scan interface=bridge address-range=192.168.8.0/24  duration=30s
/interface bridge host print where !local   # MACs per port
```

ip-scan only finds hosts in a subnet the router has an address in. If a battery
is still on its old static `192.168.8.x`, give the router an address there too:

```
/ip address add address=192.168.8.250/24 interface=bridge comment=old-site-lan
```

and the laptop one as well (macOS: an extra manual IP on the Ethernet service,
e.g. `192.168.8.251/24`, no router).

A Sonnen battery answers on port 80 at `/api/v2/status`. Old addresses: `192.168.8.10`, `.11`, `.12` (BAT 001–003).

### What a conflict looks like

- In the isolated network: two batteries on the same IP (one MAC replaces the
  other in `/ip arp print`, the address flaps), or a battery on an address that
  is taken on the site network — compare with the site router's DHCP leases.
- On the site network: `arping -I <iface> <ip>` from a laptop there; more than
  one MAC answering means a conflict.

### Fix

In each battery's web UI (`http://<ip>`): note the current network settings,
then either switch to DHCP and pin the lease on the site router, or set a static
address outside the site router's DHCP pool. Record the final address, MAC and
pool per battery below, and update the battery URLs in the agent config
(**Configure** → Batteries).

| Battery | MAC | Address found | Final address |
|---|---|---|---|
| 001 | `c4:00:ad:e6:80:46` | `192.168.8.10` static | `192.168.88.5` static |
| 002 | `c4:00:ad:ed:e4:5f` | `192.168.8.11` static | `192.168.88.6` static |
| 003 | `cc:82:7f:2d:35:92` | `192.168.8.12` static | `192.168.88.7` static |

Found 2026-10-28 on the isolation router: all three still on their old static
addresses in `192.168.8.0/24`, unique IPs and MACs, so no conflict between them.
They were unreachable because the site network is now `192.168.88.0/24` and
nothing routes the old subnet. All answer `/api/v2/status` without a token.
BAT 003 reported `SystemStatus=Init`, `Uac=1` V and zero production and
consumption — no AC voltage on its side, to be checked separately.

Moved the same day to `.5–.7` (mask `/24`, gateway and DNS `192.168.88.1`).
The site pool was not read; `.5–.7` were picked on the assumption that the site
MikroTik (`router.lan`, MAC `2C:C8:1B:E4:EB:D3`) still has the defconf pool
`192.168.88.10–254` — leases seen from `.23` to `.215`, `.2`/`.3` are static
MikroTik APs. Verify with `/ip pool print` on `192.168.88.1` when possible.
Back on the site network all three answer at the new addresses; BAT 003 then
reported `SystemStatus=Critical Error`, `Ubat=-1`. Two Fronius inverters sit at
`192.168.88.62`/`.63`.

After a reboot all three were back on `192.168.8.10–12` (same MACs): the new
address took effect at once but was not kept across a restart. The battery
only keeps a new address once its web UI has been opened **at the new
address** right after the change — that visit confirms it. Without it the
battery falls back to the previous settings on the next reboot. So: change the
address, open `http://<new ip>` straight away, then reboot once to check. Site
`192.168.88.10` and `.12` are now taken by other devices, so the old host
numbers cannot be reused in the site subnet.

Site router (`192.168.88.x`): model ?, address ?, DHCP pool ?.

Afterwards: batteries back on the site network, router home, confirm in the UI
that Merce 5 shows telemetry for all three batteries.
