package sidebar

func ollamaLoginBootstrapJS() string {
	return `(function(){
  if (location.origin !== "https://ollama.com" || location.pathname !== "/") return;
  var key = "__fqs_ollama_signin_started";
  if (sessionStorage.getItem(key)) return;
  sessionStorage.setItem(key, "1");
  setTimeout(function(){ location.replace("/signin"); }, 500);
})();`
}
