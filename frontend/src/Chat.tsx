import { useEffect,useMemo,useRef,useState,type ReactNode } from 'react';
import {AssistantRuntimeProvider,useLocalRuntime,useAuiState,AttachmentPrimitive,type AttachmentAdapter,ThreadPrimitive,ComposerPrimitive,MessagePrimitive,ActionBarPrimitive,BranchPickerPrimitive,ErrorPrimitive,type ChatModelAdapter,type ExportedMessageRepository} from '@assistant-ui/react';
import {Markdown} from './Markdown';
import {useContext} from 'react';
import {ArtifactContext,DocumentContext} from './artifactContext';
import {workspace,importDocument,acceptedDocuments,type SearchHit} from './bridge';
import remarkGfm from 'remark-gfm';
import rehypeHighlight from 'rehype-highlight';
import {ArrowUp,Square,Copy,Check,Pencil,RotateCcw,ChevronLeft,ChevronRight,ArrowDown,Code2,Terminal,Sparkles,Cpu,Paperclip,X,FileText,PanelRightOpen} from 'lucide-react';
import {Button} from './button';
import {chat,onChunk,errorText,modelLabel,type ChatChunk,type Conversation} from './bridge';
type Props={searchLibrary:boolean;conversation:Conversation;model:string;enabled:boolean;onSave:(conversation:Conversation)=>void;onError:(text:string)=>void;onRunning:(running:boolean)=>void;flushRef:React.MutableRefObject<(()=>Promise<void>)|null>};

function Reasoning({text}:{text:string}){return <details className="reasoning"><summary>Model reasoning</summary><Markdown text={text}/></details>;}
function PendingDocument(){return <AttachmentPrimitive.Root className="attachment-chip"><FileText size={13}/><AttachmentPrimitive.Name/><AttachmentPrimitive.Remove asChild><button type="button" aria-label="Remove attachment"><X size={12}/></button></AttachmentPrimitive.Remove></AttachmentPrimitive.Root>;}
function SentAttachment(){return <AttachmentPrimitive.Root className="attachment-chip"><FileText size={13}/><AttachmentPrimitive.Name/></AttachmentPrimitive.Root>;}
function OpenArtifact(){const open=useContext(ArtifactContext);const content=useAuiState(state=>state.message.content);const text=content.filter(part=>part.type==='text').map(part=>part.type==='text'?part.text:'').join('\n');return <Button variant="ghost" size="small" disabled={!text} onClick={()=>open?.({id:'',title:'Conversation answer',kind:'markdown',language:'',content:text})}><PanelRightOpen size={13}/>Open as artifact</Button>;}
function Sources(){const open=useContext(DocumentContext);const metadata=useAuiState(state=>state.message.metadata.custom);const sources=(metadata?.sources||[]) as SearchHit[];return sources.length?<div className="source-chips"><span>Sources</span>{sources.map(source=><button type="button" key={source.label} onClick={()=>open?.(source.documentID)} title={source.text}>[{source.label}] {source.name} · {source.chunk}</button>)}</div>:null;}
function Branches(){return <BranchPickerPrimitive.Root hideWhenSingleBranch className="branches"><BranchPickerPrimitive.Previous aria-label="Previous version"><ChevronLeft size={13}/></BranchPickerPrimitive.Previous><BranchPickerPrimitive.Number/> / <BranchPickerPrimitive.Count/><BranchPickerPrimitive.Next aria-label="Next version"><ChevronRight size={13}/></BranchPickerPrimitive.Next></BranchPickerPrimitive.Root>;}
function UserMessage(){return <MessagePrimitive.Root className="message user-message"><div className="user-bubble"><MessagePrimitive.Attachments components={{Attachment:SentAttachment}}/><MessagePrimitive.Content/></div><div className="message-actions"><Branches/><ActionBarPrimitive.Root><ActionBarPrimitive.Edit asChild><Button variant="ghost" size="icon" aria-label="Edit message"><Pencil size={13}/></Button></ActionBarPrimitive.Edit></ActionBarPrimitive.Root></div></MessagePrimitive.Root>;}
function AssistantMessage(){return <MessagePrimitive.Root className="message assistant-message"><div className="assistant-heading"><span className="mini-logo">L·</span><strong>Local AI</strong><span>ON YOUR MACHINE</span></div><div className="assistant-body"><MessagePrimitive.Content components={{Text:Markdown,Reasoning}}/><MessagePrimitive.Error><div className="inline-error" role="alert"><ErrorPrimitive.Message/></div></MessagePrimitive.Error><MessagePrimitive.If hasContent={false}><div className="thinking"><i/><i/><i/></div></MessagePrimitive.If><Sources/></div><div className="message-actions"><Branches/><ActionBarPrimitive.Root hideWhenRunning><ActionBarPrimitive.Copy asChild><Button variant="ghost" size="icon" aria-label="Copy response"><Copy size={14}/></Button></ActionBarPrimitive.Copy><ActionBarPrimitive.Reload asChild><Button variant="ghost" size="icon" aria-label="Retry response"><RotateCcw size={14}/></Button></ActionBarPrimitive.Reload><OpenArtifact/></ActionBarPrimitive.Root></div></MessagePrimitive.Root>;}
function EditComposer(){return <ComposerPrimitive.Root className="edit-composer"><ComposerPrimitive.Input autoFocus aria-label="Edit your message"/><div><ComposerPrimitive.Cancel asChild><Button variant="ghost" size="small">Cancel</Button></ComposerPrimitive.Cancel><ComposerPrimitive.Send asChild><Button size="small">Save & resend</Button></ComposerPrimitive.Send></div></ComposerPrimitive.Root>;}
function Welcome(){
  const suggestions=[{icon:Code2,title:'Build something useful',text:'Help me design a small Go CLI project. Start with a practical idea and a simple architecture.'},{icon:Terminal,title:'Understand a concept',text:'Explain Go goroutines and channels with a short practical example.'},{icon:Sparkles,title:'Improve my code',text:'What should I look for when reviewing Go code for clarity and reliability?'}];
  return <div className="welcome"><div className="welcome-mark">L<span>·</span></div><div className="eyebrow">A LITTLE MORE LOCAL. A LOT MORE POSSIBLE.</div><h1>What’s on your mind?</h1><p>A quiet space to think, build, and explore.<br/>Powered by the models running on your machine.</p><div className="suggestions">{suggestions.map(s=><ThreadPrimitive.Suggestion key={s.title} prompt={s.text} method="replace" autoSend={false} className="suggestion"><s.icon size={19}/><strong>{s.title}</strong><span>{s.text}</span><ArrowUp size={14}/></ThreadPrimitive.Suggestion>)}</div></div>;
}
function revive(repository:unknown):ExportedMessageRepository|null{
  if(!repository||typeof repository!=='object'||!('messages'in repository))return null;
  const copy=JSON.parse(JSON.stringify(repository)) as ExportedMessageRepository;
  copy.messages=copy.messages.map(item=>({...item,message:{...item.message,createdAt:new Date(item.message.createdAt),...(item.message.role==='assistant'&&item.message.status.type==='running'?{status:{type:'incomplete' as const,reason:'cancelled' as const}}:{})}}));
  return copy;
}
export function Chat({conversation,model,enabled,searchLibrary,onSave,onError,onRunning,flushRef}:Props){
  const current=useRef({model,onSave,onError,onRunning,searchLibrary});current.current={model,onSave,onError,onRunning,searchLibrary};
  const[metrics,setMetrics]=useState<ChatChunk|null>(null);
  const adapter=useMemo<ChatModelAdapter>(()=>({async *run({messages,abortSignal}){
    if(!current.current.model)throw new Error('Select an installed local model to start chatting.');
    const id=crypto.randomUUID(),queue:ChatChunk[]=[];let wake:(()=>void)|undefined;let text='',reasoning='';let sources:SearchHit[]=[];
    const off=onChunk(chunk=>{if(chunk.id===id){queue.push(chunk);wake?.();}});
    const notifyAbort=()=>{queue.push({id,text:'',reasoning:'',done:true,cancelled:true,tokens:0,tokensPerSecond:0});wake?.();};
    abortSignal.addEventListener('abort',notifyAbort,{once:true});
    try{
      if(abortSignal.aborted)return;setMetrics(null);
      await chat('StartContext',id,current.current.model,messages.filter(m=>m.role==='user'||m.role==='assistant'||m.role==='system').map(m=>({role:m.role,content:m.content.filter(p=>p.type==='text').map(p=>p.type==='text'?p.text:'').join('\n'),documentIDs:(m.role==='user'?m.attachments:[]).map(a=>a.id)})),current.current.searchLibrary);
      while(true){if(!queue.length)await new Promise<void>(resolve=>{wake=resolve;});wake=undefined;while(queue.length){const chunk=queue.shift()!;if(chunk.error)throw new Error(chunk.error);if(chunk.cancelled)return;if(chunk.sources)sources=chunk.sources;text+=chunk.text||'';reasoning+=chunk.reasoning||'';if(text||reasoning)yield{metadata:{custom:{sources}},content:[...(reasoning?[{type:'reasoning' as const,text:reasoning}]:[]),...(text?[{type:'text' as const,text}]:[])]};if(chunk.done){setMetrics(chunk);return;}}}
    }finally{off();abortSignal.removeEventListener('abort',notifyAbort);await chat('Cancel',id).catch(()=>{});}
  }}),[]);
  const attachments=useMemo<AttachmentAdapter>(()=>({accept:acceptedDocuments,async add({file}){try{const doc=await importDocument(file);return {id:doc.id,type:"document" as const,name:doc.name,contentType:file.type||"text/plain",file,status:{type:"requires-action" as const,reason:"composer-send" as const}};}catch(error){current.current.onError(errorText(error));throw error;}},async send(attachment){return {id:attachment.id,type:attachment.type,name:attachment.name,contentType:attachment.contentType,status:{type:"complete" as const},content:[{type:"text" as const,text:"Attached local document: "+attachment.name}]};},async remove(){}}),[]);const runtime=useLocalRuntime(adapter,{adapters:{attachments}});
  useEffect(()=>{
    const repository=revive(conversation.repository);if(repository)runtime.thread.import(repository);
    let timer:ReturnType<typeof setTimeout>|undefined,lastSaved='';let chain=Promise.resolve();
    const save=():Promise<void>=>{
      const exported=runtime.thread.export();if(!exported.messages.length)return chain;
      const serialized=JSON.stringify(exported);if(serialized===lastSaved)return chain;lastSaved=serialized;
      const first=runtime.thread.getState().messages.find(message=>message.role==='user');
      const title=first?.content.filter(p=>p.type==='text').map(p=>p.type==='text'?p.text:'').join(' ').slice(0,90)||'New conversation';
      const entry={...conversation,title,model:current.current.model,searchLibrary:current.current.searchLibrary,repository:exported};
      chain=chain.then(async()=>{try{const saved=await chat<Conversation>('Save',entry);current.current.onSave(saved);}catch(error){lastSaved='';current.current.onError(`Chat could not be saved: ${errorText(error)}`);}});return chain;
    };
    const unsubscribe=runtime.thread.subscribe(()=>{current.current.onRunning(runtime.thread.getState().isRunning);clearTimeout(timer);timer=setTimeout(save,500);});
    flushRef.current=save;
    return()=>{clearTimeout(timer);unsubscribe();void save();runtime.thread.cancelRun();flushRef.current=null;};
  },[runtime,conversation.id]);
  return <AssistantRuntimeProvider runtime={runtime}><ThreadPrimitive.Root className="chat-thread"><ThreadPrimitive.Viewport className="thread-viewport"><ThreadPrimitive.If empty><Welcome/></ThreadPrimitive.If><div className="messages"><ThreadPrimitive.Messages components={{UserMessage,AssistantMessage,EditComposer}}/></div><ThreadPrimitive.If empty={false}><ThreadPrimitive.ViewportFooter className="scroll-footer"><ThreadPrimitive.ScrollToBottom asChild><Button variant="secondary" size="icon" aria-label="Scroll to latest message"><ArrowDown size={15}/></Button></ThreadPrimitive.ScrollToBottom></ThreadPrimitive.ViewportFooter></ThreadPrimitive.If></ThreadPrimitive.Viewport><div className="composer-zone"><ComposerPrimitive.Root className="composer"><div className="composer-attachments"><ComposerPrimitive.Attachments components={{Attachment:PendingDocument}}/></div><ComposerPrimitive.Input autoFocus aria-label="Message Local AI" placeholder={enabled?'Ask anything, or paste some code…':'Start a local provider and select a model to chat'} disabled={!enabled} maxRows={7}/><div className="composer-toolbar"><span><ComposerPrimitive.AddAttachment asChild><Button variant="ghost" size="icon" aria-label="Attach document" disabled={!enabled}><Paperclip size={15}/></Button></ComposerPrimitive.AddAttachment><Cpu size={13}/>{model?modelLabel(model):'No local model selected'}</span><ThreadPrimitive.If running={false}><ComposerPrimitive.Send asChild><Button size="icon" aria-label="Send message" disabled={!enabled}><ArrowUp size={17}/></Button></ComposerPrimitive.Send></ThreadPrimitive.If><ThreadPrimitive.If running><ComposerPrimitive.Cancel asChild><Button variant="secondary" size="icon" aria-label="Stop generation"><Square size={14}/></Button></ComposerPrimitive.Cancel></ThreadPrimitive.If></div></ComposerPrimitive.Root><div className="composer-caption"><span>Enter to send <b>·</b> Shift + Enter for a new line</span><span>{metrics&&metrics.tokensPerSecond>0?`${metrics.rateBasis==="estimated"?"~":""}${metrics.tokensPerSecond.toFixed(1)} tok/s · ${metrics.tokens} tokens`:'Runs locally. Saved on this computer.'}</span></div></div></ThreadPrimitive.Root></AssistantRuntimeProvider>;
}
