# Build a GOPROXY=file:// mirror for Go modules whose vanity hosts are blocked, from GitHub copies.
import os, sys, subprocess, zipfile, json, re, shutil
S = sys.argv[1]            # scratch dir
PROXY = os.path.join(S, 'goproxy')
WORK = os.path.join(S, 'gitsrc')
os.makedirs(PROXY, exist_ok=True); os.makedirs(WORK, exist_ok=True)
# module -> (github repo, subdir, tag prefix)
MAP = {
 'golang.org/x/': ('golang/{name}', '', ''),
 'go.etcd.io/bbolt': ('etcd-io/bbolt', '', ''),
 'go.opentelemetry.io/otel': ('open-telemetry/opentelemetry-go', '', ''),
 'go.opentelemetry.io/otel/metric': ('open-telemetry/opentelemetry-go', 'metric', 'metric/'),
 'go.opentelemetry.io/otel/trace': ('open-telemetry/opentelemetry-go', 'trace', 'trace/'),
 'go.opentelemetry.io/otel/sdk': ('open-telemetry/opentelemetry-go', 'sdk', 'sdk/'),
 'go.opentelemetry.io/auto/sdk': ('open-telemetry/opentelemetry-go-instrumentation', 'sdk', 'sdk/'),
 'lukechampine.com/blake3': ('lukechampine/blake3', '', ''),
 'rsc.io/qr': ('rsc/qr', '', ''),
 'zombiezen.com/go/sqlite': ('zombiezen/go-sqlite', '', ''),
 'go.uber.org/atomic': ('uber-go/atomic', '', ''),
 'go.uber.org/multierr': ('uber-go/multierr', '', ''),
 'gopkg.in/yaml.v3': ('go-yaml/yaml', '', ''),
 'google.golang.org/protobuf': ('protocolbuffers/protobuf-go', '', ''),
 'filippo.io/edwards25519': ('FiloSottile/edwards25519', '', ''),
 'crawshaw.io/sqlite': ('crawshaw/sqlite', '', ''),
}
def lookup(mod):
    if mod in MAP: return MAP[mod]
    if mod.startswith('golang.org/x/'):
        r, sd, tp = MAP['golang.org/x/']; return (r.format(name=mod.split('/')[2]), sd, tp)
    return None
def git(*a, cwd=None):
    return subprocess.run(['git', *a], cwd=cwd, check=True, capture_output=True, text=True).stdout
def fetch(repo, ref):
    d = os.path.join(WORK, repo.replace('/', '_'))
    if not os.path.isdir(d):
        git('init', '-q', d)
        git('remote', 'add', 'origin', f'https://github.com/{repo}.git', cwd=d)
    try:
        git('fetch', '-q', '--depth', '1', 'origin', ref, cwd=d)
    except subprocess.CalledProcessError:
        git('fetch', '-q', 'origin', cwd=d)  # short commit hash: full fetch
        ref = git('rev-parse', ref, cwd=d).strip()
        git('checkout', '-q', ref, cwd=d); return d
    git('checkout', '-q', 'FETCH_HEAD', cwd=d)
    return d
def nested_mod_dirs(root):
    out = set()
    for dp, dn, fn in os.walk(root):
        if '.git' in dn: dn.remove('.git')
        if dp != root and 'go.mod' in fn: out.add(os.path.relpath(dp, root))
    return out
def build(mod, ver):
    m = lookup(mod)
    if not m: print('NO MAP', mod, ver); return False
    repo, sub, tp = m
    pm = re.match(r'.*-(\d{14})-([0-9a-f]{12})$', ver)
    ref = pm.group(2) if pm else tp + ver.replace('+incompatible', '')
    try:
        d = fetch(repo, ref)
    except subprocess.CalledProcessError as e:
        print('FETCH FAIL', mod, ver, e.stderr.strip()[:200]); return False
    root = os.path.join(d, sub) if sub else d
    skip = nested_mod_dirs(root)
    esc = re.sub(r'[A-Z]', lambda x: '!' + x.group(0).lower(), mod)
    vd = os.path.join(PROXY, esc, '@v'); os.makedirs(vd, exist_ok=True)
    zp = os.path.join(vd, ver + '.zip')
    with zipfile.ZipFile(zp, 'w', zipfile.ZIP_DEFLATED) as z:
        for dp, dn, fn in os.walk(root):
            rel = os.path.relpath(dp, root)
            dn[:] = [x for x in dn if x not in ('.git', '.hg', '.svn', '.bzr') and os.path.normpath(os.path.join(rel, x)) not in skip and not (rel == '.' and x == 'vendor')]
            for f in fn:
                p = os.path.join(dp, f)
                if os.path.islink(p) or not os.path.isfile(p): continue
                z.write(p, f'{mod}@{ver}/' + os.path.normpath(os.path.join(rel, f)))
        if sub and not os.path.exists(os.path.join(root, 'LICENSE')) and os.path.exists(os.path.join(d, 'LICENSE')):
            z.write(os.path.join(d, 'LICENSE'), f'{mod}@{ver}/LICENSE')  # Go adds the repo's LICENSE to subdir modules
    gm = os.path.join(root, 'go.mod')
    open(os.path.join(vd, ver + '.mod'), 'w').write(open(gm).read() if os.path.exists(gm) else f'module {mod}\n')
    t = git('log', '-1', '--format=%cI', cwd=d).strip()
    json.dump({'Version': ver, 'Time': t}, open(os.path.join(vd, ver + '.info'), 'w'))
    lst = os.path.join(vd, 'list'); vs = set(open(lst).read().split()) if os.path.exists(lst) else set()
    vs.add(ver); open(lst, 'w').write('\n'.join(sorted(vs)) + '\n')
    print('OK', mod, ver); return True
for arg in sys.argv[2:]:
    mod, ver = arg.split('@'); build(mod, ver)
