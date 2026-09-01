package telegramloginhttp

import (
	"crypto/sha256"
	"encoding/base64"
	"net/http"
)

// telegramLoginJavaScript intentionally follows the public Telegram Login
// SDK contract (programmatic API, data-* auto-init, popup auth_result events,
// and the Mini App oauth_* bridge). It derives the provider from its own
// script origin so the same file works on a self-hosted issuer.
const telegramLoginJavaScript = `(function(global){
'use strict';
var current=document.currentScript;
if(!current){throw new Error('Telegram Login SDK must be loaded by a script element');}
var provider=new URL(current.src,document.baseURI).origin;
var saved=null,active=null,inApp=false,inAppPending=false;
function callback(cb,value){if(typeof cb==='function'){try{cb(value);}catch(error){setTimeout(function(){throw error;},0);}}}
function decode(token){try{var part=token.split('.')[1].replace(/-/g,'+').replace(/_/g,'/');while(part.length%4){part+='=';}return JSON.parse(decodeURIComponent(Array.from(atob(part),function(c){return '%'+c.charCodeAt(0).toString(16).padStart(2,'0');}).join('')));}catch(_){return null;}}
function build(data){if(data&&data.error){return {error:String(data.error)};}var token=data&&data.result;if(typeof token!=='string'||!token){return {error:'missing id_token'};}var user=decode(token);return user?{id_token:token,user:user}:{error:'malformed id_token'};}
function normalize(options){
  if(!options||!/^[0-9]{1,64}$/.test(String(options.client_id||''))){throw new Error('Telegram.Login client_id is required');}
  var scopes=['openid'],input=options.scope;
  if(input===undefined||input===null||input===''){scopes.push('profile');input=options.request_access||[];}
  if(typeof input==='string'){input=input.trim()?input.trim().split(/\s+/):[];}
  if(!Array.isArray(input)){throw new Error('Telegram.Login scope must be an array or string');}
  var allowed={openid:'openid',profile:'profile',phone:'phone',write:'telegram:bot_access','telegram:bot_access':'telegram:bot_access'};
  input.forEach(function(value){var mapped=allowed[value];if(!mapped){throw new Error('Telegram.Login scope is invalid');}if(scopes.indexOf(mapped)<0){scopes.push(mapped);}});
  return {client_id:String(options.client_id),scope:scopes.join(' '),nonce:String(options.nonce||'').slice(0,1024),lang:String(options.lang||'').slice(0,16)};
}
function randomURL(bytes){var data=new Uint8Array(bytes);crypto.getRandomValues(data);var raw='';data.forEach(function(value){raw+=String.fromCharCode(value);});return btoa(raw).replace(/\+/g,'-').replace(/\//g,'_').replace(/=+$/,'');}
function finish(flow,result){if(active!==flow){return;}active=null;if(flow.timer){clearInterval(flow.timer);}if(flow.pollTimer){clearTimeout(flow.pollTimer);}if(flow.listener){global.removeEventListener('message',flow.listener);}callback(flow.callback,result);}
async function pollFromParent(flow,token){if(active!==flow){return;}try{var body=new URLSearchParams({browser_token:token});var response=await fetch(provider+'/auth/status',{method:'POST',headers:{'content-type':'application/x-www-form-urlencoded'},body:body,credentials:'omit',cache:'no-store'});var data=await response.json();if(!response.ok){throw new Error(data.error||'request_failed');}if(data.status==='pending'){flow.pollTimer=setTimeout(function(){pollFromParent(flow,token);},1000);return;}finish(flow,data.id_token?build({result:data.id_token}):{error:data.error||data.status});}catch(error){finish(flow,{error:error.message||'login_failed'});}}
function sendEvent(type,data){if(global.TelegramWebviewProxy&&typeof global.TelegramWebviewProxy.postEvent==='function'){global.TelegramWebviewProxy.postEvent(type,JSON.stringify(data||{}));}}
async function receiveEvent(type,data){
  if(type==='oauth_supported'){inApp=true;return;}
  if(type==='oauth_result_failed'){if(active){finish(active,{error:'access_denied'});}return;}
  if(type!=='oauth_result_confirmed'||!active||!data||!data.result_url){return;}
  try{var resultURL=new URL(data.result_url);if(resultURL.origin!==provider||resultURL.pathname!=='/inapp'){throw new Error('invalid in-app result URL');}var token=resultURL.searchParams.get('token');if(!token){throw new Error('missing in-app token');}
    var response=await fetch(provider+'/inapp?code='+encodeURIComponent(token),{credentials:'omit',cache:'no-store'}),result=await response.json();
    if(!response.ok){throw new Error(result.error||'in-app exchange failed');}finish(active,build(result));
  }catch(error){finish(active,{error:error.message||'in_app_failed'});}
}
function begin(options,cb,isNormalized){
  var normalized;try{normalized=isNormalized?options:normalize(options);}catch(error){callback(cb,{error:error.message});return null;}
  if(active){callback(cb,{error:'login_in_progress'});return null;}
  var flow={popup:null,callback:cb,listener:null,timer:null,pollTimer:null,browserToken:''};active=flow;
  if(inApp){
    if(inAppPending){finish(flow,{error:'login_in_progress'});return null;}inAppPending=true;
    var inAppParams=new URLSearchParams({scope:normalized.scope,origin:global.location.origin,client_id:normalized.client_id,response_type:'id_token'});
    fetch(provider+'/inapp?'+inAppParams.toString(),{credentials:'omit',cache:'no-store'}).then(function(response){return response.json().then(function(body){if(!response.ok){throw new Error(body.error||'in-app request failed');}return body;});}).then(function(body){if(!body.url){throw new Error('missing OAuth deep link');}sendEvent('oauth_request',{url:body.url});}).catch(function(error){finish(flow,{error:error.message||'in_app_failed'});}).finally(function(){setTimeout(function(){inAppPending=false;},600);});
    return null;
  }
  var popup=global.open('about:blank','telegram-login-'+randomURL(8),'popup,width=550,height=650,resizable=yes,scrollbars=yes');
  if(!popup){finish(flow,{error:'popup_blocked'});return null;}flow.popup=popup;
  flow.listener=function(event){if(event.origin!==provider||event.source!==popup){return;}var data=event.data;try{if(typeof data==='string'){data=JSON.parse(data);}}catch(_){return;}if(!data){return;}if(data.event==='auth_pending'){var token=String(data.browser_token||'');if(!/^[A-Za-z0-9_-]{43}$/.test(token)){finish(flow,{error:'invalid_browser_token'});return;}if(flow.browserToken&&flow.browserToken!==token){finish(flow,{error:'invalid_browser_token'});return;}if(!flow.browserToken){flow.browserToken=token;pollFromParent(flow,token);}return;}if(data.event!=='auth_result'){return;}finish(flow,build(data));};
  global.addEventListener('message',flow.listener);flow.timer=setInterval(function(){if(popup.closed&&!flow.browserToken){finish(flow,{error:'popup_closed'});}},500);
  try{var params=new URLSearchParams({client_id:normalized.client_id,redirect_uri:global.location.origin+global.location.pathname,response_type:'post_message',scope:normalized.scope});if(normalized.nonce){params.set('nonce',normalized.nonce);}if(normalized.lang){params.set('lang',normalized.lang);}popup.location.replace(provider+'/auth?'+params.toString());}
  catch(error){try{popup.close();}catch(_){}finish(flow,{error:error.message||'login_failed'});}
  return popup;
}
var api={
  init:function(options,cb){saved={options:normalize(options),callback:cb};return api;},
  open:function(cb){if(!saved){callback(cb,{error:'not_initialized'});return null;}return begin(saved.options,cb||saved.callback,true);},
  auth:function(options,cb){return begin(options,cb,false);},
  close:function(){if(active&&active.popup){try{active.popup.close();}catch(_){}}if(active){finish(active,{error:'popup_closed'});}}
};
global.Telegram=global.Telegram||{};global.Telegram.Login=api;
global.Telegram.WebView=global.Telegram.WebView||{};global.Telegram.WebView.receiveEvent=receiveEvent;
global.Telegram.TelegramGameProxy=global.Telegram.TelegramGameProxy||{};global.Telegram.TelegramGameProxy.receiveEvent=receiveEvent;
if(global.TelegramWebviewProxy){sendEvent('oauth_request',{});}
document.addEventListener('click',function(event){var node=event.target;while(node&&node!==document){if(node.classList&&node.classList.contains('tg-auth-button')){api.open();return;}node=node.parentNode;}});
function resolveCallback(source){var match=/^([A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*)\s*\(\s*data\s*\)\s*;?$/.exec(source||'');if(!match){return null;}return function(data){var target=global,parts=match[1].split('.');for(var i=0;i<parts.length-1;i++){target=target&&target[parts[i]];}var fn=target&&target[parts[parts.length-1]];if(typeof fn==='function'){fn.call(target,data);}};}
function autoInit(){var client=current.getAttribute('data-client-id');if(!client){return;}var options={client_id:client},access=current.getAttribute('data-request-access'),lang=current.getAttribute('data-lang');if(access){options.request_access=access.trim().split(/\s+/);}if(lang){options.lang=lang;}api.init(options,resolveCallback(current.getAttribute('data-onauth')));}
if(document.readyState==='loading'){document.addEventListener('DOMContentLoaded',autoInit);}else{autoInit();}
})(window);`

// telegramWidgetJavaScript is deliberately only a presentation and
// compatibility layer. It loads this issuer's Telegram Login SDK, optionally
// resolves a local bot username, and delegates the authorization flow to
// Telegram.Login.auth without contacting Telegram's public widget backend.
const telegramWidgetJavaScript = `(function(global){
'use strict';
var current=document.currentScript;
if(!current){throw new Error('telesrv Telegram widget shim must be loaded by a script element');}
var provider=new URL(current.src,document.baseURI).origin;
var clientAttr=String(current.getAttribute('data-client-id')||'').trim();
var username=String(current.getAttribute('data-telegram-login')||'').trim().replace(/^@/,'');
var size=String(current.getAttribute('data-size')||'large').trim().toLowerCase();
var radius=String(current.getAttribute('data-radius')||'8').trim();
var requestAccess=String(current.getAttribute('data-request-access')||'').trim().toLowerCase();
var lang=String(current.getAttribute('data-lang')||'').trim().slice(0,16);
var onauth=resolveCallback(current.getAttribute('data-onauth'));
var label=username?'Log in with @'+username:'Log in with Telegram';
var wrapper=document.createElement('span'),button=document.createElement('button');
wrapper.className='telesrv-telegram-widget';
button.type='button';button.className='telesrv-telegram-login-button';button.disabled=true;button.textContent=label;
button.style.cssText='border:0;background:#2aabee;color:#fff;cursor:pointer;font-family:Arial,sans-serif;font-weight:600;line-height:1.2;box-shadow:0 1px 2px rgba(0,0,0,.18)';
var sizes={small:['12px','6px 10px'],medium:['14px','8px 14px'],large:['16px','10px 18px']},selected=sizes[size]||sizes.large;
button.style.fontSize=selected[0];button.style.padding=selected[1];
button.style.borderRadius=/^[0-9]{1,3}$/.test(radius)?Math.min(Number(radius),64)+'px':'8px';
if(lang){button.lang=lang;}
wrapper.appendChild(button);
function mount(){if(current.parentNode&&current.parentNode!==document.head){current.parentNode.insertBefore(wrapper,current.nextSibling);}else if(document.body){document.body.appendChild(wrapper);}}
if(document.body){mount();}else{document.addEventListener('DOMContentLoaded',mount,{once:true});}
function resolveCallback(source){
  var match=/^([A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*)(?:\(\s*(?:[A-Za-z_$][\w$]*)?\s*\))?\s*;?$/.exec(String(source||'').trim());
  if(!match){return null;}
  return function(value){var target=global,parts=match[1].split('.');for(var i=0;i<parts.length-1;i++){target=target&&target[parts[i]];}var fn=target&&target[parts[parts.length-1]];if(typeof fn==='function'){try{fn.call(target,value);}catch(error){setTimeout(function(){throw error;},0);}}};
}
function finish(result){
  if(result&&result.error){button.disabled=false;button.textContent=label;button.title=String(result.error);}else{button.textContent='Logged in';button.title='';}
  if(onauth){onauth(result);}
}
function fail(error){var message=error&&error.message?error.message:String(error||'login_failed');finish({error:message});}
function loadLoginSDK(){
  var state=global.__telesrvTelegramLoginSDKState;
  if(state&&state.provider===provider){return state.promise;}
  var promise=new Promise(function(resolve,reject){
    var script=document.createElement('script');script.async=true;script.src=provider+'/js/telegram-login.js';
    script.onload=function(){var api=global.Telegram&&global.Telegram.Login;if(!api||typeof api.auth!=='function'){reject(new Error('telesrv Telegram Login SDK is unavailable'));return;}resolve(api);};
    script.onerror=function(){reject(new Error('failed to load telesrv Telegram Login SDK'));};
    (document.head||document.documentElement).appendChild(script);
  });
  global.__telesrvTelegramLoginSDKState={provider:provider,promise:promise};
  return promise;
}
function resolveClientID(){
  if(clientAttr){return /^[0-9]{1,64}$/.test(clientAttr)?Promise.resolve(clientAttr):Promise.reject(new Error('invalid data-client-id'));}
  if(!/^[A-Za-z][A-Za-z0-9_]{3,31}$/.test(username)){return Promise.reject(new Error('data-telegram-login or data-client-id is required'));}
  var body=new URLSearchParams({username:username});
  return fetch(provider+'/telegram-widget/resolve',{method:'POST',headers:{'content-type':'application/x-www-form-urlencoded'},body:body,credentials:'omit',cache:'no-store'}).then(function(response){
    return response.json().catch(function(){return {};}).then(function(data){if(!response.ok){throw new Error(data.error_description||data.error||'widget client is unavailable');}if(!data.client_id||!/^[0-9]{1,64}$/.test(String(data.client_id))){throw new Error('invalid widget client response');}return String(data.client_id);});
  });
}
var api=null,clientID='';
Promise.all([loadLoginSDK(),resolveClientID()]).then(function(values){api=values[0];clientID=values[1];button.disabled=false;button.title='';}).catch(fail);
button.addEventListener('click',function(){
  if(!api||!clientID){return;}
  button.disabled=true;button.textContent='Opening...';button.title='';
  var scope=requestAccess==='write'?'openid profile telegram:bot_access':'openid profile';
  var options={client_id:clientID,scope:scope};if(lang){options.lang=lang;}
  try{api.auth(options,finish);}catch(error){fail(error);}
});
})(window);`

func (h *Handler) loginJavaScript(w http.ResponseWriter, r *http.Request) {
	serveJavaScript(w, r, telegramLoginJavaScript)
}

func (h *Handler) widgetJavaScript(w http.ResponseWriter, r *http.Request) {
	serveJavaScript(w, r, telegramWidgetJavaScript)
}

func serveJavaScript(w http.ResponseWriter, r *http.Request, source string) {
	sum := sha256.Sum256([]byte(source))
	etag := `"` + base64.RawURLEncoding.EncodeToString(sum[:]) + `"`
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=3600, must-revalidate")
	w.Header().Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(source))
}
