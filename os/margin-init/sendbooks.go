package main

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"margin-init/epub"
)

// Send to Books: a small web page the tablet serves on its Wi-Fi address. A phone (or any
// computer) on the same network opens it, picks EPUB files, and they land in the Library
// (internal storage's Books folder), checked to open first. Only devices on the local
// network are answered.

const maxUpload = 300 << 20 // per book

var sendPort = 0 // the port the page is on, once it listens (80, or 8080 if 80 is taken)

// sendAddress is what to type in the phone's browser, or "" without Wi-Fi.
func sendAddress() string {
	ip := wifiAddr()
	if ip == "" || sendPort == 0 {
		return ""
	}
	if sendPort == 80 {
		return ip
	}
	return fmt.Sprintf("%s:%d", ip, sendPort)
}

// serveBooks runs the page. It doesn't return.
func (c *console) serveBooks() {
	mux := http.NewServeMux()
	mux.HandleFunc("/", c.sendPage)
	mux.HandleFunc("/upload", c.sendUpload)
	mux.HandleFunc("/soma-link", c.somaLinkPost)
	mux.HandleFunc("/github-link", c.githubLinkPost)
	srv := &http.Server{Handler: localOnly(mux)}
	for _, port := range []int{80, 8080} {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err != nil {
			log.Printf("send to books: port %d: %v", port, err)
			continue
		}
		sendPort = port
		log.Printf("send to books: http://<wifi address>:%d", port)
		log.Printf("send to books: %v", srv.Serve(ln))
		return
	}
}

// localOnly answers devices on the local network (and the tablet itself) only.
func localOnly(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		if ip == nil || !(ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
			http.Error(w, "only for devices on the tablet's network", http.StatusForbidden)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// safeBookName keeps a file name's letters (any script), digits and simple punctuation.
func safeBookName(name string) string {
	name = filepath.Base(strings.ReplaceAll(name, "\\", "/"))
	name = strings.TrimSuffix(name, filepath.Ext(name))
	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsMark(r) || unicode.IsDigit(r) || strings.ContainsRune(" -_.,'()&!", r):
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	s := strings.Trim(strings.TrimSpace(b.String()), ".")
	if s == "" {
		s = "book"
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s + ".epub"
}

// freeName is dir/name, or "name (2).epub" and so on if that's taken.
func freeName(dir, name string) string {
	p := filepath.Join(dir, name)
	for i := 2; ; i++ {
		if _, err := os.Stat(p); os.IsNotExist(err) {
			return p
		}
		p = filepath.Join(dir, fmt.Sprintf("%s (%d).epub", strings.TrimSuffix(name, ".epub"), i))
	}
}

type sendResult struct {
	Saved  []string `json:"saved"`
	Errors []string `json:"errors"`
}

func (c *console) sendUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST books here", http.StatusMethodNotAllowed)
		return
	}
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "send the books as a form upload", http.StatusBadRequest)
		return
	}
	os.MkdirAll(storeDir, 0o755)
	var res sendResult
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			res.Errors = append(res.Errors, "the upload was cut off: "+err.Error())
			break
		}
		if part.FileName() == "" {
			continue
		}
		name := part.FileName()
		if !strings.EqualFold(filepath.Ext(name), ".epub") {
			res.Errors = append(res.Errors, name+": not an EPUB (only .epub books for now)")
			part.Close()
			continue
		}
		dst := freeName(storeDir, safeBookName(name))
		if err := saveBook(part, dst); err != nil {
			res.Errors = append(res.Errors, name+": "+err.Error())
		} else {
			res.Saved = append(res.Saved, filepath.Base(dst))
			log.Printf("send to books: saved %s", dst)
		}
		part.Close()
	}
	if len(res.Saved) > 0 {
		c.booksArrived(len(res.Saved))
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(res)
}

// saveBook writes one upload to dst, keeping it only if it opens as an EPUB.
func saveBook(src io.Reader, dst string) error {
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(src, maxUpload+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	switch {
	case err != nil:
		os.Remove(tmp)
		return err
	case n > maxUpload:
		os.Remove(tmp)
		return fmt.Errorf("bigger than %d MB", maxUpload>>20)
	}
	b, err := epub.Open(tmp)
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("doesn't open as an EPUB (%v)", err)
	}
	b.Close()
	return os.Rename(tmp, dst)
}

// booksArrived refreshes the screen if it shows the Library or Home, and says so.
func (c *console) booksArrived(n int) {
	drawMu.Lock()
	defer drawMu.Unlock()
	msg := "1 book received"
	if n > 1 {
		msg = fmt.Sprintf("%d books received", n)
	}
	c.toastMsg(msg)
	if c.screenOn && (c.mode == modeBooks || c.mode == modeBooksHome) {
		c.showPage()
	}
}

// somaLinkPost links the Soma tab from a phone: the server address and recovery code are
// pasted from Soma's Settings > Sync on the phone, so nobody types 56 characters on the
// tablet. Only the page itself may send them (not another site the phone has open).
func (c *console) somaLinkPost(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	reply := func(code int, v map[string]any) {
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(v)
	}
	if r.Method != http.MethodPost {
		reply(http.StatusMethodNotAllowed, map[string]any{"error": "POST the address and code here"})
		return
	}
	if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
		reply(http.StatusForbidden, map[string]any{"error": "only this page can link Soma"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := r.ParseForm(); err != nil {
		reply(http.StatusBadRequest, map[string]any{"error": "bad form"})
		return
	}
	st := soma
	if err := st.setLink(r.PostForm.Get("url"), r.PostForm.Get("code")); err != nil {
		reply(http.StatusOK, map[string]any{"error": err.Error()})
		return
	}
	log.Printf("soma: linked from %s", r.RemoteAddr)
	go func() {
		st.sync()
		drawMu.Lock()
		defer drawMu.Unlock()
		c.toastMsg("Soma linked")
		if c.screenOn && c.mode == modeSoma {
			c.somaUI.msg = ""
			c.showPage()
		}
	}()
	reply(http.StatusOK, map[string]any{"ok": true})
}

func (c *console) sendPage(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	var list strings.Builder
	for _, b := range findBooks() {
		fmt.Fprintf(&list, "<li><b>%s</b><span>%s</span></li>", html.EscapeString(b.title), html.EscapeString(b.author))
	}
	status := "Not linked yet."
	if soma.linked() {
		status = "Linked. " + soma.status() + ". Paste again to link to another Soma."
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page := strings.Replace(sendHTML, "<!--BOOKS-->", list.String(), 1)
	page = strings.Replace(page, "<!--SOMA-->", html.EscapeString(status), 1)
	io.WriteString(w, strings.Replace(page, "<!--GITHUB-->", html.EscapeString(githubLinkStatus()), 1))
}

const sendHTML = `<!doctype html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Send to Margin Books</title>
<style>
:root{color-scheme:dark;--bg:#000;--card:#1c1c1e;--fill:#2c2c2e;--sep:#38383a;--label:#fff;--second:#8d8d93;--blue:#0a84ff;--green:#32d74b;--red:#ff453a}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--label);font:17px/1.4 -apple-system,BlinkMacSystemFont,"SF Pro Text",Inter,Roboto,sans-serif;padding:24px 16px 48px;max-width:640px;margin:auto}
h1{font:700 34px/1.1 "New York",Georgia,serif;margin:8px 0 4px}
p.sub{color:var(--second);margin:0 0 24px}
.card{background:var(--card);border-radius:16px;padding:20px;margin-bottom:24px}
label.pick{display:block;border:2px dashed var(--sep);border-radius:14px;padding:28px 16px;text-align:center;color:var(--second);cursor:pointer}
label.pick b{display:block;color:var(--blue);font-size:19px;margin-bottom:4px}
input[type=file]{display:none}
button{width:100%;margin-top:16px;border:0;border-radius:14px;padding:15px;font:600 17px/1 inherit;background:var(--blue);color:#fff}
button:disabled{background:var(--fill);color:var(--second)}
#files{margin:12px 0 0;color:var(--second);font-size:15px}
.bar{height:6px;background:var(--fill);border-radius:3px;margin-top:14px;overflow:hidden;display:none}
.bar i{display:block;height:100%;width:0;background:var(--blue)}
#msg{margin-top:12px;font-size:15px}
.ok{color:var(--green)}.err{color:var(--red)}
h2{font:600 22px/1.2 inherit;margin:0 0 12px}
ul{list-style:none;margin:0;padding:0}
li{padding:12px 0;border-top:1px solid var(--sep)}
li:first-child{border-top:0}
li span{display:block;color:var(--second);font-size:15px}
input[type=url],input[type=password]{width:100%;margin-top:10px;border:0;border-radius:12px;padding:14px;background:var(--fill);color:var(--label);font:17px/1.2 inherit}
#smsg{margin-top:12px;font-size:15px}
</style></head><body>
<h1>Send to Books</h1>
<p class="sub">Pick EPUB books on this phone: they appear in Margin's Library.</p>
<div class="card">
<label class="pick"><b>Choose books</b>EPUB files, as many as you like
<input id="f" type="file" accept=".epub,application/epub+zip" multiple></label>
<div id="files"></div>
<button id="go" disabled>Send</button>
<div class="bar"><i></i></div>
<div id="msg"></div>
</div>
<div class="card"><h2>On the tablet</h2><ul><!--BOOKS--></ul></div>
<div class="card" id="soma"><h2>Link Soma</h2>
<p class="sub" style="margin-bottom:12px">In Soma on this phone: Settings &rsaquo; Sync. Copy the server address and the recovery code, and paste them here. The tablet's Soma tab then stays in step with your phone and PC. <span id="sstate"><!--SOMA--></span></p>
<input id="surl" type="url" placeholder="Server address (https://…)" autocapitalize="off" autocorrect="off" spellcheck="false">
<input id="scode" type="password" placeholder="Recovery code" autocapitalize="characters" autocorrect="off" spellcheck="false" autocomplete="off">
<button id="slink">Link Soma</button>
<div id="smsg"></div>
</div>
<div class="card" id="github"><h2>Link GitHub</h2>
<p class="sub" style="margin-bottom:12px">So the tablet can send the output of Clipboard commands to a <b>private</b> repo the AI can read. On github.com: create a private repo (default <b>aminoulogie/margin-results</b>, tick "Add a README"), then Settings &rsaquo; Developer settings &rsaquo; Fine-grained tokens: give it that repo (and margin, to publish fixes) with Contents: read and write. Paste the token here. <span><!--GITHUB--></span></p>
<input id="gtoken" type="password" placeholder="Token (github_pat_…)" autocapitalize="off" autocorrect="off" spellcheck="false" autocomplete="off">
<input id="grepo" type="url" placeholder="Results repo (aminoulogie/margin-results)" autocapitalize="off" autocorrect="off" spellcheck="false">
<button id="glink">Link GitHub</button>
<div id="gmsg"></div>
</div>
<script>
var f=document.getElementById('f'),go=document.getElementById('go'),msg=document.getElementById('msg'),
bar=document.querySelector('.bar'),fill=document.querySelector('.bar i'),files=document.getElementById('files');
f.onchange=function(){var n=f.files.length;go.disabled=!n;files.textContent=n?n+(n>1?' books':' book')+' chosen':'';msg.textContent=''};
go.onclick=function(){
  var d=new FormData();for(var i=0;i<f.files.length;i++)d.append('book',f.files[i]);
  var x=new XMLHttpRequest();x.open('POST','/upload');
  go.disabled=true;bar.style.display='block';fill.style.width='0';msg.textContent='Sending…';msg.className='';
  x.upload.onprogress=function(e){if(e.lengthComputable)fill.style.width=(100*e.loaded/e.total)+'%'};
  x.onload=function(){var r={};try{r=JSON.parse(x.responseText)}catch(e){}
    var s=(r.saved||[]).length,errs=r.errors||[];
    msg.innerHTML=(s?'<div class="ok">'+s+(s>1?' books are':' book is')+' in the Library.</div>':'')+
      errs.map(function(e){return '<div class="err">'+e.replace(/</g,'&lt;')+'</div>'}).join('');
    if(s)setTimeout(function(){location.reload()},1500);else go.disabled=false};
  x.onerror=function(){msg.textContent='Sending failed: is the phone on the same Wi-Fi as the tablet?';msg.className='err';go.disabled=false};
  x.send(d)};
var sl=document.getElementById('slink'),sm=document.getElementById('smsg');
sl.onclick=function(){
  var u=document.getElementById('surl').value.trim(),k=document.getElementById('scode').value.trim();
  if(!u||!k){sm.textContent='Paste both: the server address and the recovery code.';sm.className='err';return}
  var x=new XMLHttpRequest();x.open('POST','/soma-link');
  x.setRequestHeader('Content-Type','application/x-www-form-urlencoded');
  sl.disabled=true;sm.textContent='Linking…';sm.className='';
  x.onload=function(){var r={};try{r=JSON.parse(x.responseText)}catch(e){}
    sl.disabled=false;
    if(r.ok){sm.textContent='Linked: open the Soma tab on the tablet.';sm.className='ok';document.getElementById('scode').value=''}
    else{sm.textContent=r.error||'Linking failed.';sm.className='err'}};
  x.onerror=function(){sl.disabled=false;sm.textContent='Is the phone on the same Wi-Fi as the tablet?';sm.className='err'};
  x.send('url='+encodeURIComponent(u)+'&code='+encodeURIComponent(k))};
var gl=document.getElementById('glink'),gm=document.getElementById('gmsg');
gl.onclick=function(){
  var t=document.getElementById('gtoken').value.trim(),rp=document.getElementById('grepo').value.trim();
  if(!t){gm.textContent='Paste the token.';gm.className='err';return}
  var x=new XMLHttpRequest();x.open('POST','/github-link');
  x.setRequestHeader('Content-Type','application/x-www-form-urlencoded');
  gl.disabled=true;gm.textContent='Checking with GitHub…';gm.className='';
  x.onload=function(){var r={};try{r=JSON.parse(x.responseText)}catch(e){}
    gl.disabled=false;
    if(r.ok){gm.textContent='Linked: outputs go to '+r.repo+'.';gm.className='ok';document.getElementById('gtoken').value=''}
    else{gm.textContent=r.error||'Linking failed.';gm.className='err'}};
  x.onerror=function(){gl.disabled=false;gm.textContent='Is the phone on the same Wi-Fi as the tablet?';gm.className='err'};
  x.send('token='+encodeURIComponent(t)+'&repo='+encodeURIComponent(rp))};
</script></body></html>`
