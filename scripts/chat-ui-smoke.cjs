// Tests production React assets with a test-only Wails bridge. No fixtures ship in the app.
const {chromium}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../frontend/dist'),threads=new Map(),subscribers=new Set(),jobs=new Map();
let online=true,modelAvailable=true,starts=0,cancels=0;
const push=data=>{for(const res of subscribers)res.write(`data: ${JSON.stringify(data)}\n\n`);};
const snapshot=()=>({timestamp:new Date().toISOString(),gpus:[{id:'fixture',name:'RTX 5080',memoryUsed:2048,memoryTotal:16303,utilization:10,temperature:40,power:55}],gpuError:'',ollamaOnline:online,ollamaError:online?'':'Ollama offline',models:online&&modelAvailable?[{name:'test-coder:7b',size:5e9,details:{parameter_size:'7B',quantization_level:'Q4_K_M'}}]:[],running:[]});
const server=http.createServer((req,res)=>{
  if(req.url==='/wails/runtime.js'){
    res.setHeader('Content-Type','text/javascript');res.end(`const callbacks=new Set();const source=new EventSource('/__events');source.onmessage=e=>{for(const cb of callbacks)cb({data:JSON.parse(e.data)})};export const Events={On:(name,cb)=>{callbacks.add(cb);return()=>callbacks.delete(cb)}};export const Call={ByName:async(name,...args)=>{const res=await fetch('/__call',{method:'POST',body:JSON.stringify({name,args})});const data=await res.json();if(!res.ok)throw new Error(data.error);return data;}};`);return;
  }
  if(req.url==='/__events'){res.writeHead(200,{'Content-Type':'text/event-stream','Cache-Control':'no-cache','Connection':'keep-alive'});res.write(': connected\n\n');subscribers.add(res);req.on('close',()=>subscribers.delete(res));return;}
  if(req.url==='/__call'){
    let body='';req.on('data',chunk=>body+=chunk);req.on('end',()=>{
      const {name,args}=JSON.parse(body);res.setHeader('Content-Type','application/json');
      const method=name.split('.').at(-1);let result=null;
      if(method==='Snapshot')result=snapshot();
      else if(method==='History')result=[];
      else if(method==='Conversations')result=[...threads.values()].map(x=>({...x,repository:null}));
      else if(method==='Conversation')result=threads.get(args[0]);
      else if(method==='Save'){const value={...args[0],updated:new Date().toISOString()};threads.set(value.id,value);result={...value,repository:null};}
      else if((method==='Start'||method==='StartContext')){
        starts++;const[id,model,messages]=args;
        if(!online){res.writeHead(500);res.end(JSON.stringify({error:'Ollama offline'}));return;}
        const slow=messages.at(-1).content.includes('slow');
        const timer=setTimeout(()=>{
          push({id,text:'Here is a Go example.\n\n',reasoning:'',done:false});
          if(!slow){const finish=setTimeout(()=>{push({id,text:'```go\nfunc hello() string { return "hello" }\n```\n\n<script>untrusted()</script>',reasoning:'',done:false});push({id,text:'',reasoning:'',done:true,tokens:100,tokensPerSecond:50});jobs.delete(id);},180);jobs.set(id,finish);}
        },150);jobs.set(id,timer);
      } else if(method==='Cancel'){const id=args[0];if(jobs.has(id)){clearTimeout(jobs.get(id));jobs.delete(id);cancels++;push({id,text:'',done:true,cancelled:true});}}
      res.end(JSON.stringify(result));
    });return;
  }
  const relative=decodeURIComponent(req.url.split('?')[0]);
  const file=path.resolve(root,'.'+(relative==='/'?'/index.html':relative));
  if(!file.startsWith(root+path.sep)||!fs.existsSync(file)||fs.statSync(file).isDirectory()){res.writeHead(404);return res.end();}
  const ext=path.extname(file);res.setHeader('Content-Type',({'.html':'text/html','.js':'text/javascript','.css':'text/css','.svg':'image/svg+xml'})[ext]||'application/octet-stream');res.end(fs.readFileSync(file));
});
(async()=>{
  await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve));let browser,page;
  try{
    browser=await chromium.launch({headless:true,channel:'msedge'});
    const context=await browser.newContext({viewport:{width:1240,height:860},permissions:['clipboard-read','clipboard-write']});
    page=await context.newPage();const errors=[];page.on('pageerror',error=>errors.push(error.message));
    await page.goto(`http://127.0.0.1:${server.address().port}`);
    await page.getByRole('textbox',{name:'Message Local AI'}).waitFor();
    await page.waitForFunction(()=>document.querySelector('select[aria-label="Chat model"]').value==='test-coder:7b');
    await page.screenshot({path:path.resolve(__dirname,'../../chat-welcome-check.png')});
    await page.getByRole('button',{name:/Build something useful/}).click();
    assert((await page.getByRole('textbox',{name:'Message Local AI'}).inputValue()).includes('Go CLI'));
    await page.getByRole('textbox',{name:'Message Local AI'}).fill('Explain a Go palindrome.');
    await page.getByRole('button',{name:'Send message',exact:true}).click();
    await page.getByRole('button',{name:'Stop generation',exact:true}).waitFor();
    await page.locator('.code-block').waitFor();
    await page.waitForFunction(()=>document.querySelector('.composer-caption').textContent.includes('50.0 tok/s'));
    assert.equal(await page.locator('.assistant-body script').count(),0);
    await page.getByRole('button',{name:'Copy code',exact:true}).click();
    assert((await page.evaluate(()=>navigator.clipboard.readText())).includes('func hello'));
    await page.waitForFunction(()=>document.querySelector('.conversation-list').textContent.includes('Explain a Go palindrome.'));
    await page.screenshot({path:path.resolve(__dirname,'../../chat-response-check.png')});
    await page.getByRole('button',{name:'Retry response',exact:true}).click();
    await page.waitForFunction(()=>!document.querySelector('button[aria-label="Stop generation"]'));
    assert.equal(starts,2);
    await page.getByRole('button',{name:'Edit message',exact:true}).click();
    await page.getByRole('textbox',{name:'Edit your message',exact:true}).fill('Explain Go channels.');
    await page.getByRole('button',{name:'Save & resend',exact:true}).click();
    await page.waitForFunction(()=>document.querySelector('.user-bubble')?.textContent==='Explain Go channels.');
    await page.waitForFunction(()=>!document.querySelector('button[aria-label="Stop generation"]'));
    assert.equal(starts,3);
    await page.getByRole('button',{name:'New conversation',exact:false}).click();
    await page.getByText('What’s on your mind?',{exact:true}).waitFor();
    await page.getByRole('textbox',{name:'Message Local AI'}).fill('slow response please');
    await page.getByRole('button',{name:'Send message',exact:true}).click();
    await page.getByRole('button',{name:'Stop generation',exact:true}).click();
    await page.waitForFunction(()=>!document.querySelector('button[aria-label="Stop generation"]'));
    assert(cancels>0);
    await page.getByRole('button',{name:'New conversation',exact:false}).click();
    await page.getByText('What’s on your mind?',{exact:true}).waitFor();
    await page.locator('.conversation-list button').filter({hasText:'Explain Go channels.'}).click();
    await page.waitForFunction(()=>document.querySelector('.user-bubble')?.textContent==='Explain Go channels.');
    await page.reload();
    await page.waitForFunction(()=>document.querySelector('.user-bubble')?.textContent==='Explain Go channels.');
    await page.getByRole('button',{name:'Hardware & models',exact:true}).click();
    await page.frameLocator('iframe').locator('#gpu-cards').getByText('RTX 5080').waitFor();
    await page.getByRole('button',{name:'Chat',exact:true}).click();
    online=false;await page.getByRole('button',{name:'Refresh models',exact:true}).click();
    await page.getByText('Local model servers are offline.',{exact:false}).waitFor();
    assert(await page.getByRole('textbox',{name:'Message Local AI'}).isDisabled());
    online=true;modelAvailable=false;await page.getByRole('button',{name:'Refresh models',exact:true}).click();
    await page.getByText('No local models available for this provider.',{exact:false}).waitFor();
    await page.setViewportSize({width:900,height:650});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
    assert.deepEqual(errors,[]);
    console.log('Chat UI passed: streaming, stop, markdown/code copy, retry/edit, history restore across reload, dashboard, offline/no-model states, 900px layout.');
  }catch(error){console.error("UI DEBUG", await page.locator("body").innerText());await page.screenshot({path:path.resolve(__dirname,"../../chat-failure-check.png")});throw error;}finally{if(browser)await browser.close();for(const timer of jobs.values())clearTimeout(timer);server.closeAllConnections();server.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
