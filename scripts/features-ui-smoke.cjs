// Tests production React assets with a test-only Wails bridge. No fixtures ship in the app.
const {chromium}=require('playwright');
const http=require('node:http'),fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict');
const root=path.resolve(__dirname,'../frontend/dist'),threads=new Map(),subscribers=new Set(),jobs=new Map();
let online=true,modelAvailable=true,starts=0,cancels=0;const documents=new Map(),artifacts=new Map(),comparisons=[];let compareActive='';let lastChatArgs;
const push=data=>{for(const res of subscribers)res.write(`data: ${JSON.stringify(data)}\n\n`);};
const snapshot=()=>({timestamp:new Date().toISOString(),gpus:[{id:'fixture',name:'RTX 5080',memoryUsed:2048,memoryTotal:16303,utilization:10,temperature:40,power:55}],gpuError:'',ollamaOnline:online,ollamaError:online?'':'Ollama offline',models:online&&modelAvailable?[{name:'test-coder:7b',size:5e9,details:{parameter_size:'7B',quantization_level:'Q4_K_M'}},{name:'test-coder:14b',size:9e9,details:{parameter_size:'14B'}}]:[],running:[]});
const server=http.createServer((req,res)=>{
  if(req.url==='/wails/runtime.js'){
    res.setHeader('Content-Type','text/javascript');res.end(`const callbacks=new Set();const source=new EventSource('/__events');source.onmessage=e=>{for(const cb of callbacks)cb({data:JSON.parse(e.data)})};export const Events={On:(name,cb)=>{callbacks.add(cb);return()=>callbacks.delete(cb)}};export const Call={ByName:async(name,...args)=>{const res=await fetch('/__call',{method:'POST',body:JSON.stringify({name,args})});const data=await res.json();if(!res.ok)throw new Error(data.error);return data;}};`);return;
  }
  if(req.url==='/__events'){res.writeHead(200,{'Content-Type':'text/event-stream','Cache-Control':'no-cache','Connection':'keep-alive'});res.write(': connected\n\n');subscribers.add(res);req.on('close',()=>subscribers.delete(res));return;}
  if(req.url==='/__call'){
    let body='';req.on('data',chunk=>body+=chunk);req.on('end',()=>{
      const {name,args}=JSON.parse(body);res.setHeader('Content-Type','application/json');
      const method=name.split('.').at(-1);let result=null;
      if(name.includes('WorkspaceService')){
        if(method==='Import'){
          const id='a'.repeat(31)+(documents.size?'2':'1');const text=Buffer.from(args[1],'base64').toString();const doc={id,name:args[0],kind:'md',size:text.length,imported:new Date().toISOString(),text,chunks:[text]};documents.set(id,doc);result={...doc,text:undefined,chunks:undefined};
        }else if(method==='Documents')result=[...documents.values()].map(doc=>({...doc,text:undefined,chunks:undefined}));
        else if(method==='Document')result=documents.get(args[0]);
        else if(method==='Search')result=[...documents.values()].filter(doc=>doc.text.toLowerCase().includes(args[0].split(' ')[0].toLowerCase())).map(doc=>({documentID:doc.id,name:doc.name,chunk:1,text:doc.text,score:1,label:'D1'}));
        else if(method==='SaveArtifact'){const artifact={...args[0],id:args[0].id||'b'.repeat(32),updated:new Date().toISOString()};artifacts.set(artifact.id,artifact);result=artifact;}
        else if(method==='Artifacts')result=[...artifacts.values()].map(artifact=>({...artifact,content:''}));
        else if(method==='Artifact')result=artifacts.get(args[0]);
        else if(method==='Export')result='G:\\Projects\\Palindrome snippet.go';
        res.end(JSON.stringify(result));return;
      }
      if(name.includes('ComparisonService')){
        if(method==='History')result=comparisons;
        else if(method==='Get')result=comparisons.find(item=>item.id===args[0]);
        else if(method==='Cancel'){compareActive='';push({id:args[0],complete:true,cancelled:true});}
        else if(method==='Start'){
          const[id,prompt,models]=args;compareActive=id;
          const record={id:'c'.repeat(32),prompt,created:new Date().toISOString(),results:[]};
          models.forEach((model,index)=>setTimeout(()=>{
            if(compareActive!==id)return;
            const result={model,response:'Comparison answer '+(index+1),tokens:25,tokensPerSecond:50-index*10,seconds:1.2+index};record.results.push(result);push({id,index,model,result});
            if(index===models.length-1){comparisons.unshift(record);compareActive='';push({id,complete:true});}
          },500*(index+1)));
        }
        res.end(JSON.stringify(result));return;
      }
      if(method==='Snapshot')result=snapshot();
      else if(method==='History')result=[];
      else if(method==='Conversations')result=[...threads.values()].map(x=>({...x,repository:null}));
      else if(method==='Conversation')result=threads.get(args[0]);
      else if(method==='Save'){const value={...args[0],updated:new Date().toISOString()};threads.set(value.id,value);result={...value,repository:null};}
      else if((method==='Start'||method==='StartContext')){
        starts++;lastChatArgs=args;const[id,model,messages]=args;
        if(!online){res.writeHead(500);res.end(JSON.stringify({error:'Ollama offline'}));return;}
        const slow=messages.at(-1).content.includes('slow');
        const timer=setTimeout(()=>{if((args[3]||messages.some(m=>m.documentIDs?.length))&&documents.size){const doc=[...documents.values()][0];push({id,text:'',sources:[{documentID:doc.id,name:doc.name,chunk:1,text:doc.text,score:1,label:'D1'}]});}
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
    page=await browser.newPage({viewport:{width:1240,height:860}});
    const errors=[];page.on('pageerror',error=>errors.push(error.message));
    const external=[];page.on('request',request=>{if(request.url().includes('example.invalid'))external.push(request.url());});
    await page.goto(`http://127.0.0.1:${server.address().port}`);
    await page.getByRole('textbox',{name:'Message Local AI'}).waitFor();
    await page.getByRole('button',{name:'Document library',exact:true}).click();
    const fixture={name:'reference.md',mimeType:'text/markdown',buffer:Buffer.from('# Aurora\nLaunch code is ORBIT-42. The project uses Go and Wails.')};
    await page.locator('input[aria-label="Import documents"]').setInputFiles(fixture);
    await page.locator('.document-card').getByText('reference.md',{exact:true}).waitFor();
    await page.getByRole('textbox',{name:'Search document contents'}).fill('Launch code');
    await page.getByRole('button',{name:'Search',exact:true}).click();
    await page.locator('.search-hit').filter({hasText:'ORBIT-42'}).waitFor();
    await page.locator('.search-hit').first().click();
    await page.locator('.document-text').filter({hasText:'ORBIT-42'}).waitFor();
    await page.getByRole('button',{name:'Close document preview'}).click();
    await page.getByRole('button',{name:'Chat',exact:true}).click();
    const chooserPromise=page.waitForEvent('filechooser');
    await page.getByRole('button',{name:'Attach document',exact:true}).click();
    await (await chooserPromise).setFiles(fixture);
    await page.locator('.composer-attachments .attachment-chip').filter({hasText:'reference.md'}).waitFor();
    await page.getByRole('textbox',{name:'Message Local AI'}).fill('What is the launch code?');
    await page.getByRole('button',{name:'Send message',exact:true}).click();
    await page.locator('.source-chips button').filter({hasText:'reference.md'}).waitFor();
    assert(lastChatArgs[2].at(-1).documentIDs.length===1);
    await page.locator('.source-chips button').first().click();
    await page.locator('.document-text').filter({hasText:'ORBIT-42'}).waitFor();
    await page.getByRole('button',{name:'Close document preview'}).click();
    await page.waitForFunction(()=>!document.querySelector('button[aria-label="Stop generation"]'));
    await page.getByRole('button',{name:'Preview code artifact',exact:true}).first().click();
    await page.getByRole('complementary',{name:'Artifact preview'}).waitFor();
    await page.getByRole('textbox',{name:'Artifact title'}).fill('Palindrome snippet');
    await page.getByRole('button',{name:'Save artifact',exact:true}).click();
    await page.getByText('Saved to artifact library.',{exact:true}).waitFor();
    await page.getByRole('button',{name:'Export',exact:true}).click();
    await page.getByRole('status').filter({hasText:'Exported to'}).waitFor();
    await page.getByRole('button',{name:'Close artifact preview'}).click();
    await page.getByRole('button',{name:'Artifact library',exact:true}).click();
    await page.locator('.document-card').filter({hasText:'Palindrome snippet'}).click();
    await page.getByRole('combobox',{name:'Artifact type'}).selectOption('html');
    await page.getByRole('button',{name:'Source',exact:true}).click();
    await page.getByRole('textbox',{name:'Artifact source'}).fill('<h1>Safe preview</h1><script>window.top.__bad=true</script><img src="https://example.invalid/track"><meta http-equiv="refresh" content="0;url=https://example.invalid"><a href="https://example.invalid">link</a>');
    await page.getByRole('button',{name:'Preview',exact:true}).click();
    const frame=page.frameLocator('iframe[title="Sandboxed HTML artifact"]');
    await frame.getByText('Safe preview',{exact:true}).waitFor();
    assert.equal(await frame.locator('script, [href], [src], meta[http-equiv="refresh"]').count(),0);
    assert.equal(await page.evaluate(()=>window.__bad),undefined);assert.deepEqual(external,[]);
    await page.screenshot({path:path.resolve(__dirname,'../../artifact-preview-check.png')});
    await page.getByRole('button',{name:'Close artifact preview'}).click();
    await page.getByRole('button',{name:'Model comparison',exact:true}).click();
    await page.getByRole('textbox',{name:'Comparison prompt'}).fill('Compare Go approaches.');
    await page.getByRole('button',{name:'Run comparison',exact:true}).click();
    await page.getByRole('button',{name:'Chat',exact:true}).click();
    assert(await page.getByRole('textbox',{name:'Message Local AI'}).isDisabled());
    await page.getByRole('button',{name:'Model comparison',exact:true}).click();
    await page.getByText('Comparison complete. Results saved locally.',{exact:true}).waitFor();
    assert.equal(await page.locator('.comparison-result').count(),2);
    await page.locator('.comparison-history button').first().click();
    await page.getByText('Viewing saved results.',{exact:true}).waitFor();
    await page.screenshot({path:path.resolve(__dirname,'../../model-comparison-check.png')});
    await page.getByRole('button',{name:'Run comparison',exact:true}).click();
    await page.getByRole('button',{name:'Stop comparison',exact:true}).click();
    await page.getByText('Comparison stopped. Partial results were saved.',{exact:true}).waitFor();
    await page.getByRole('button',{name:'Chat',exact:true}).click();
    await page.getByRole('checkbox',{name:/Search my document library/}).check();
    await page.getByRole('textbox',{name:'Message Local AI'}).fill('Find the launch code.');
    await page.getByRole('button',{name:'Send message',exact:true}).click();
    await page.waitForFunction(()=>!document.querySelector('button[aria-label="Stop generation"]'));
    assert.equal(lastChatArgs[3],true);
    await page.setViewportSize({width:900,height:650});assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth));
    assert.deepEqual(errors,[]);
    console.log('Feature UI passed: document import/search/preview, chat attachments, cited sources, artifact save/export, sanitized HTML, sequential comparison/results/history/stop, library toggle, 900px layout.');
  }catch(error){if(page){console.error('UI DEBUG',await page.locator('body').innerText());await page.screenshot({path:path.resolve(__dirname,'../../feature-failure.png')});}throw error;}
  finally{if(browser)await browser.close();for(const timer of jobs.values())clearTimeout(timer);server.closeAllConnections();server.close();}
})().catch(error=>{console.error(error);process.exitCode=1;});
