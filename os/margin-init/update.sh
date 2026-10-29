#!/bin/sh
# update: Margin updates itself, from the tablet alone (Terminal tab, or ssh). It gets the
# latest code from GitHub (an archive, with curl: new git can't run on this kernel, it needs
# getrandom(), which 3.4 doesn't have), builds margin-init here with Go, then installs it the way
# wifi-update does: a backup, a restart, and the old version put back if the new one
# doesn't run. margin-init installs this as /usr/local/bin/update in Alpine; the first time
# (or from an older version) it can also be fetched:
#   curl -fsSLo /tmp/u https://raw.githubusercontent.com/aminoulogie/margin/main/os/margin-init/update.sh
#   sh /tmp/u
#
# The tablet keeps the names its /system startup hook uses: margin-init is installed as
# /data/condor/condor-init (and so runs as "condor-init"). Renaming those means rewriting the
# hook, which needs the PC (margin takeover install-hook), so they stay.
#
#   update           get the latest code from GitHub, build it, install it
#   update --local   build and install the code already in /root/margin-src as it is (fixes
#                    made on the tablet, by the Terminal's Claude tab, say)
#   update --force   get GitHub's code even over changes made here that aren't on GitHub
REPO=aminoulogie/margin
BRANCH=main
SRC=/root/margin-src
D=/proc/1/root/data/condor # /data/condor as init sees it (we're in Alpine's chroot)

say() { printf '\033[1;36m==> %s\033[0m\n' "$*"; }
die() { printf '\033[1;31m%s\033[0m\n' "$*"; exit 1; }

LOCAL=0 FORCE=0
for a in "$@"; do
	case $a in
	--local) LOCAL=1 ;;
	--force) FORCE=1 ;;
	*) die "update: unknown option $a (update, update --local, update --force)" ;;
	esac
done

[ "$(id -u)" = 0 ] || die "run it as root"
[ -f $D/condor-init ] || die "can't see $D: run it on the tablet"

if ! command -v go >/dev/null || ! command -v curl >/dev/null; then
	say "installing Go and curl (once, about 200 MB)"
	apk add go curl || die "apk failed: is Wi-Fi on?"
fi

if [ $LOCAL = 1 ]; then
	[ -d $SRC/os/margin-init ] || die "no code in $SRC yet: run update once (without --local)"
	say "building the code in $SRC as it is (--local)"
else
	# The code here can hold fixes made on the tablet (the Claude tab) that aren't on GitHub:
	# .margin-manifest has the md5 of every file as it was downloaded.
	if [ $FORCE = 0 ] && [ -f $SRC/.margin-manifest ] && ! (cd $SRC && md5sum -c .margin-manifest >/dev/null 2>&1); then
		die "$SRC has changes that aren't on GitHub (fixes from the Claude tab). Publish them from the Claude tab first, or: update --local (build them as they are), update --force (download over them)"
	fi
	say "getting the latest code"
	# fetch tries a few times, through margin-init's proxy and then straight out: a connection to
	# GitHub can drop for a minute after Wi-Fi joins, or on one route and not the other.
	fetch() {
		for i in 1 2 3 4 5; do
			curl -fsSL "$@" && return 0
			curl -fsSL --noproxy '*' "$@" && return 0
			echo "    retrying ($i)..." >&2; sleep 3
		done
		return 1
	}
	commit=$(fetch -H "Accept: application/vnd.github.sha" https://api.github.com/repos/$REPO/commits/$BRANCH 2>/dev/null)
	rm -rf $SRC.new /tmp/margin-src.tgz && mkdir -p $SRC.new
	fetch -o /tmp/margin-src.tgz https://codeload.github.com/$REPO/tar.gz/refs/heads/$BRANCH &&
		tar -xzf /tmp/margin-src.tgz -C $SRC.new || die "download failed: is Wi-Fi on?"
	rm -f /tmp/margin-src.tgz
	rm -rf $SRC && mv $SRC.new/* $SRC && rm -rf $SRC.new || die "couldn't unpack the code"
	echo "    $BRANCH at ${commit:-?}"
	(cd $SRC && find . -type f ! -name .margin-manifest -exec md5sum {} + >.margin-manifest)
fi

say "building margin-init (the first time takes several minutes)"
mkdir -p /root/.cache/gotmp
rm -f /tmp/margin-init.new # a leftover (an earlier update) would stop the build
cd $SRC/os/margin-init || die "no margin-init in the code"
# GOTMPDIR on flash: /tmp is memory. GOTOOLCHAIN=auto fetches a newer Go if go.mod needs one.
GOTMPDIR=/root/.cache/gotmp GOTOOLCHAIN=auto GOOS=linux GOARCH=386 CGO_ENABLED=0 \
	go build -o /tmp/margin-init.new . || die "the build failed (nothing was changed)"
want=$(md5sum /tmp/margin-init.new | cut -d' ' -f1)
if [ "$want" = "$(md5sum $D/condor-init | cut -d' ' -f1)" ]; then
	say "already up to date"
	exit 0
fi

# The install runs detached: restarting margin-init closes this terminal.
cat >/tmp/margin-install.sh <<'EOF'
want=$1
D=/proc/1/root/data/condor
log() { echo "$(date +%T) $*"; }
restart() {
	touch $D/takeover
	for p in $(pidof condor-init); do kill $p; done
	i=0
	while pidof condor-init >/dev/null && [ $i -lt 20 ]; do sleep 0.5; i=$((i+1)); done
	/system/bin/start flash_recovery
}
cp $D/condor-init $D/condor-init.bak || { log "FAILED: couldn't keep a backup"; exit 1; }
cp /tmp/margin-init.new $D/condor-init.new && chmod 755 $D/condor-init.new && mv -f $D/condor-init.new $D/condor-init || {
	log "FAILED: couldn't install"; exit 1; }
[ "$(md5sum $D/condor-init | cut -d' ' -f1)" = "$want" ] || {
	cp $D/condor-init.bak $D/condor-init; log "FAILED: wrong md5 after install, old one kept"; exit 1; }
log "installed, restarting margin-init"
restart
sleep 15
if pidof condor-init >/dev/null; then
	rm -f /tmp/margin-init.new
	log "DONE: the new margin-init is running (pid $(pidof condor-init))"
	exit 0
fi
log "the new margin-init didn't start: putting the old one back"
cp $D/condor-init.bak $D/condor-init
restart
sleep 10
if pidof condor-init >/dev/null; then log "ROLLED BACK: the old version is running again"; else
	log "FAILED: nothing running. Plug in USB and run update.cmd on the PC"; fi
EOF
# Run by Claude (the Claude tab): the restarted Margin opens that tab again, to carry on.
[ -n "$MARGIN_FROM_CLAUDE" ] && touch $D/claude-reopen
say "installing: the screen restarts in a few seconds"
echo "    afterwards, in Terminal: cat /tmp/margin-update.log"
sleep 2
setsid sh /tmp/margin-install.sh "$want" >/tmp/margin-update.log 2>&1 </dev/null &
