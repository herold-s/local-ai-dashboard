import {useEffect,useRef,useState} from 'react';
import {createRoot} from 'react-dom/client';
import {MessageSquare,Plus,Search,LayoutDashboard,ChevronDown,HardDrive,Cpu,PanelLeftClose,PanelLeftOpen,RefreshCw,Library,FileCode2,GitCompareArrows} from 'lucide-react';
import {Chat} from './Chat';
import {Button} from './button';
import {DocumentsView,ArtifactsView,ArtifactPreview,DocumentPreview} from './Workspace';
import {CompareView} from './Compare';
import {ArtifactContext,DocumentContext} from './artifactContext';
import {chat,monitor,errorText,modelLabel,type Conversation,type Snapshot,type Artifact} from './bridge';
import 'highlight.js/styles/github-dark.css';
import './style.css';
import './features.css';

const fresh=():Conversation=>({id:crypto.randomUUID(),title:'New conversation',model:'',updated:new Date().toISOString(),repository:null});
type View='chat'|'documents'|'artifacts'|'compare'|'dashboard';
function App(){
  const [view,setView]=useState<View>('chat');
  const [conversations,setConversations]=useState<Conversation[]>([]);
  const [conversation,setConversation]=useState<Conversation>(fresh);
  const [snapshot,setSnapshot]=useState<Snapshot|null>(null);
  const [model,setModel]=useState('');
  const [provider,setProvider]=useState('all');
  const [error,setError]=useState('');
  const [running,setRunning]=useState(false);
  const [comparing,setComparing]=useState(false);
  const [loading,setLoading]=useState(false);
  const [query,setQuery]=useState('');
  const [sidebar,setSidebar]=useState(true);
  const [ready,setReady]=useState(false);
  const [searchLibrary,setSearchLibrary]=useState(false);
  const [artifact,setArtifact]=useState<Artifact|null>(null);
  const [documentID,setDocumentID]=useState('');
  const [artifactRevision,setArtifactRevision]=useState(0);
  const flushRef=useRef<(()=>Promise<void>)|null>(null);
  const latestModel=useRef('');
  const refreshing=useRef(false);
  latestModel.current=model;
  const busy=running||comparing;
  const refresh=async()=>{
    if(refreshing.current)return;
    refreshing.current=true;
    try{
      const state=await monitor<Snapshot>('Snapshot');
      setSnapshot(state);
      const local=state.models.filter(m=>!m.name.toLowerCase().includes('cloud')&&!m.remote_model&&!m.remote_host);
      if(!latestModel.current&&local.length)setModel(local[0].name);
    }catch(err){setError(`Unable to read local status: ${errorText(err)}`);}
    finally{refreshing.current=false;}
  };
  useEffect(()=>{
    void refresh();
    const interval=setInterval(()=>{if(!document.hidden)void refresh();},5000);
    chat<Conversation[]>('Conversations').then(async items=>{
      setConversations(items);
      if(items.length){
        const saved=await chat<Conversation>('Conversation',items[0].id);
        setConversation(saved);setSearchLibrary(!!saved.searchLibrary);
        if(saved.model)setModel(saved.model);
      }
    }).catch(err=>setError(`Cannot load saved chats: ${errorText(err)}`)).finally(()=>setReady(true));
    return()=>clearInterval(interval);
  },[]);
  const allLocalModels=snapshot?.models.filter(m=>!m.name.toLowerCase().includes('cloud')&&!m.remote_model&&!m.remote_host)||[];
  const localModels=allLocalModels.filter(item=>provider==='all'||(item.provider||'ollama')===provider);
  const online=snapshot?.inferenceOnline??snapshot?.ollamaOnline??false;
  const onlineNames=snapshot?.providers?.filter(item=>item.online).map(item=>item.name).join(' + ')||(snapshot?.ollamaOnline?'Ollama':'Local providers');
  const selectedExists=localModels.some(m=>m.name===model);
  const saved=(item:Conversation)=>{
    setConversations(previous=>[item,...previous.filter(c=>c.id!==item.id)].sort((a,b)=>b.updated.localeCompare(a.updated)));
    setConversation(previous=>previous.id===item.id?{...previous,title:item.title,model:item.model,updated:item.updated}:previous);
  };
  const select=async(id?:string)=>{
    if(busy||loading)return;
    setLoading(true);setError('');
    try{
      await flushRef.current?.();
      const next=id?await chat<Conversation>('Conversation',id):fresh();
      setConversation(next);setSearchLibrary(!!next.searchLibrary);
      if(next.model)setModel(next.model);
      setView('chat');
    }catch(err){setError(errorText(err));}finally{setLoading(false);}
  };
  const openArtifact=(value:Artifact)=>{setDocumentID('');setArtifact(value);};
  const openDocument=(id:string)=>{setArtifact(null);setDocumentID(id);};
  const gpuMemory=snapshot?.gpus.filter(g=>g.memoryUsed!=null).reduce((sum,g)=>sum+g.memoryUsed!,0)||0;
  const navigation=[{id:'chat',name:'Chat',icon:MessageSquare},{id:'documents',name:'Document library',icon:Library},{id:'artifacts',name:'Artifact library',icon:FileCode2},{id:'compare',name:'Model comparison',icon:GitCompareArrows},{id:'dashboard',name:'Hardware & models',icon:LayoutDashboard}] as const;
  const pageTitle=view==='chat'?conversation.title:navigation.find(item=>item.id===view)?.name;
  return <ArtifactContext.Provider value={openArtifact}><DocumentContext.Provider value={openDocument}>
    <div className={`app-shell ${sidebar?'':'sidebar-collapsed'}`}>
      <aside className="app-sidebar">
        <a className="brand" href="#" onClick={event=>event.preventDefault()}><span className="brand-logo">L<span>·</span></span><span>LOCAL AI<small>YOUR INFERENCE WORKSPACE</small></span></a>
        <Button className="new-chat" variant="secondary" onClick={()=>void select()} disabled={busy||loading||!ready}><Plus size={16}/>New conversation<span>↗</span></Button>
        <nav className="workspace-nav">{navigation.map(item=><button key={item.id} className={view===item.id?'selected':''} onClick={()=>setView(item.id)}><item.icon size={15}/>{item.name}</button>)}</nav>
        <div className="history-label"><span>CONVERSATIONS</span><span>{conversations.length||''}</span></div>
        <div className="search-box"><Search size={13}/><input aria-label="Search conversations" placeholder="Search conversations" value={query} onChange={event=>setQuery(event.target.value)}/></div>
        <div className="conversation-list">{conversations.filter(c=>c.title.toLowerCase().includes(query.toLowerCase())).map(item=><button disabled={busy||loading} key={item.id} className={item.id===conversation.id?'active':''} onClick={()=>void select(item.id)} title={item.title}><MessageSquare size={13}/><span>{item.title}</span></button>)}{!conversations.length&&<div className="history-empty">Your conversations will<br/>find a home here.</div>}{conversations.length>0&&!conversations.some(c=>c.title.toLowerCase().includes(query.toLowerCase()))&&<div className="history-empty">No matching conversations.</div>}</div>
        <div className="local-card"><span className={`status-dot ${online?'':'offline'}`}/><strong>{online?onlineNames+' connected':'Local providers offline'}</strong><p>Your models. Your hardware.<br/>Your conversations stay here.</p><code>{provider==='fastflowlm'?'127.0.0.1:52625':'Ollama + FastFlowLM'}</code></div>
        <div className="sidebar-foot"><span>GO + WAILS V3</span><span>LOCAL FIRST</span></div>
      </aside>
      <main className="app-main">
        <header className="app-header"><div className="header-title"><Button variant="ghost" size="icon" aria-label="Toggle sidebar" onClick={()=>setSidebar(!sidebar)}>{sidebar?<PanelLeftClose size={17}/>:<PanelLeftOpen size={17}/>}</Button><span className="header-divider"/><span>Workspace<span className="slash">/</span><strong>{pageTitle}</strong></span></div><div className="header-status"><span className={`status-dot ${online?'':'offline'}`}/><span>{online?'Local inference':'Offline'}</span><span className="avatar">LA</span></div></header>
        {error&&<div className="app-error" role="alert">{error}<button aria-label="Dismiss error" onClick={()=>setError('')}>×</button></div>}
        <div className="chat-view" style={{display:view==='chat'?'flex':'none'}}>
          <div className="chat-toolbar"><div className="model-control"><select aria-label="Inference provider" value={provider} disabled={busy} onChange={event=>{const value=event.target.value;setProvider(value);const first=allLocalModels.find(item=>value==="all"||(item.provider||"ollama")===value);setModel(first?.name||"");}}><option value="all">All providers</option><option value="ollama">Ollama</option><option value="fastflowlm">FastFlowLM</option></select><Cpu size={15}/><select aria-label="Chat model" value={model} disabled={busy||!localModels.length} onChange={event=>{setModel(event.target.value);setError('');}}>{!selectedExists&&<option value={model}>{model?`${model} (unavailable)`:'Choose a local model'}</option>}{localModels.map(item=><option key={item.name} value={item.name}>{modelLabel(item.name)}</option>)}</select><ChevronDown size={12}/><Button variant="ghost" size="icon" aria-label="Refresh models" onClick={()=>void refresh()}><RefreshCw size={13}/></Button></div><span className="hardware-chip"><HardDrive size={12}/>{snapshot?.gpus.length||0} GPU{snapshot?.gpus.length===1?'':'s'}<span>·</span>{(gpuMemory/1024).toFixed(1)} GB VRAM used</span></div>
          <label className="library-toggle"><input type="checkbox" checked={searchLibrary} disabled={busy} onChange={event=>setSearchLibrary(event.target.checked)}/><Library size={13}/>Search my document library<span>Answers use matching excerpts with source references</span></label>
          {comparing&&<div className="connection-notice">Model comparison is running. Stop it or wait before sending a chat message.</div>}
          {snapshot&&!online&&<div className="connection-notice">Local model servers are offline. Start Ollama or FastFlowLM to resume chatting. Saved conversations are still available.</div>}
          {online&&!localModels.length&&<div className="connection-notice">No local models available for this provider. Start its server with an installed model, then refresh.</div>}
          {ready?<Chat key={conversation.id} conversation={conversation} model={model} searchLibrary={searchLibrary} enabled={online&&selectedExists&&!loading&&!comparing} onSave={saved} onError={setError} onRunning={setRunning} flushRef={flushRef}/>:<div className="loading-state">Opening your workspace…</div>}
        </div>
        {view==='documents'&&<DocumentsView onOpen={openDocument} onError={setError}/>}
        {view==='artifacts'&&<ArtifactsView onOpen={openArtifact} onError={setError} revision={artifactRevision}/>}
        <div className="compare-container" style={{display:view==='compare'?'flex':'none'}}><CompareView models={allLocalModels} chatRunning={running||!online} onBusy={setComparing} onError={setError} onArtifact={openArtifact}/></div>
        {view==='dashboard'&&<iframe className="dashboard-frame" src="/monitor/index.html?embedded=1" title="Hardware and model dashboard"/>}
      </main>
      {artifact&&<ArtifactPreview artifact={artifact} onChange={setArtifact} onClose={()=>setArtifact(null)} onSaved={()=>setArtifactRevision(previous=>previous+1)} onError={setError}/>}
      {documentID&&<DocumentPreview id={documentID} onClose={()=>setDocumentID('')} onError={setError}/>}
    </div>
  </DocumentContext.Provider></ArtifactContext.Provider>;
}
createRoot(document.getElementById('root')!).render(<App/>);
