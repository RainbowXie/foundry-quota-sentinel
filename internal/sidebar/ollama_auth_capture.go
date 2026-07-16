package sidebar

func ollamaAuthCaptureJS() string {
	return `(function(){
  function send(k,v){try{if(v&&window.__ocgtOllamaCandidate)window.__ocgtOllamaCandidate(String(k||''),String(v))}catch(e){}}
  function inspect(k,v){
    if(typeof v!=='string')return;
    var cookie=/(?:^|[;\s])(__Secure-session=[^;\s]+)/.exec(v);
    if(cookie)send(k,cookie[1]);
    if(/^[A-Za-z0-9._\-]{30,800}$/.test(v))send(k,v);
    if(v.charAt(0)==='{'||v.charAt(0)==='['){try{var o=JSON.parse(v);for(var n in o)inspect(n,o[n])}catch(e){}}
  }
  function scan(store,label){try{for(var i=0;i<store.length;i++){var k=store.key(i);inspect(label+':'+k,store.getItem(k))}}catch(e){}}
  function header(k,v){if(String(k).toLowerCase()!=='authorization'&&String(k).toLowerCase()!=='cookie')return;inspect('header:'+k,String(v));}
  try{var set=XMLHttpRequest.prototype.setRequestHeader;XMLHttpRequest.prototype.setRequestHeader=function(k,v){header(k,v);return set.apply(this,arguments)}}catch(e){}
  try{var fetch=window.fetch;window.fetch=function(input,init){try{var h=init&&init.headers;if(h&&h.get){header('authorization',h.get('authorization'));header('cookie',h.get('cookie'))}else if(h){for(var k in h)header(k,h[k])}}catch(e){}return fetch.apply(this,arguments)}}catch(e){}
  scan(localStorage,'local');scan(sessionStorage,'session');
  var n=0,t=setInterval(function(){scan(localStorage,'local');scan(sessionStorage,'session');if(++n>=15)clearInterval(t)},2000);
})();`
}
