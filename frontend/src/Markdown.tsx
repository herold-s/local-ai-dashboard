import {useContext,useRef,useState,isValidElement,type ReactNode} from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeHighlight from 'rehype-highlight';
import {Code2,Copy,Check,PanelRightOpen} from 'lucide-react';
import {ArtifactContext} from './artifactContext';

function CodeBlock({children}:{children?:ReactNode}){
  const ref=useRef<HTMLPreElement>(null),preview=useContext(ArtifactContext);
  const[copied,setCopied]=useState(false);
  const language=isValidElement<{className?:string}>(children)?children.props.className?.match(/language-([\w+-]+)/)?.[1]||'text':'text';
  return <div className="code-block"><div className="code-heading"><span><Code2 size={13}/>{language}</span><div>{preview&&<button type="button" aria-label="Preview code artifact" onClick={()=>preview({id:'',title:`${language} snippet`,kind:['html','json','csv'].includes(language)?language:'code',language,content:ref.current?.innerText||''})}><PanelRightOpen size={13}/>Preview</button>}<button type="button" aria-label="Copy code" onClick={async()=>{try{await navigator.clipboard.writeText(ref.current?.innerText||'');setCopied(true);setTimeout(()=>setCopied(false),1800);}catch{setCopied(false);}}}>{copied?<Check size={13}/>:<Copy size={13}/>} {copied?'Copied':'Copy'}</button></div></div><pre ref={ref}>{children}</pre></div>;
}
export function Markdown({text}:{text:string}){return <div className="markdown"><ReactMarkdown remarkPlugins={[remarkGfm]} rehypePlugins={[rehypeHighlight]} components={{pre:CodeBlock,a:({children,href})=><span className="response-link" title={href}>{children}{href&&<small> ({href})</small>}</span>,img:({alt})=><span>[Image: {alt||'external image'}]</span>}}>{text}</ReactMarkdown></div>;}
