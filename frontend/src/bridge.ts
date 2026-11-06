// @ts-expect-error Wails supplies this module at runtime.
import { Call, Events } from '/wails/runtime.js';
export const monitor = <T>(method:string,...args:unknown[]):Promise<T> => Call.ByName(`main.MonitorService.${method}`,...args);
export const chat = <T>(method:string,...args:unknown[]):Promise<T> => Call.ByName(`main.ChatService.${method}`,...args);
export const onChunk=(callback:(chunk:ChatChunk)=>void):(()=>void)=>Events.On('chat:chunk',(event:{data:ChatChunk})=>callback(event.data));
export interface ChatChunk {id:string;text:string;reasoning:string;done:boolean;cancelled:boolean;error?:string;tokensPerSecond:number;tokens:number;sources?:SearchHit[];rateBasis?:string}
export interface Conversation {id:string;title:string;model:string;updated:string;repository:unknown;searchLibrary?:boolean}
export interface Model {name:string;size:number;provider?:string;modelID?:string;remote_model?:string;remote_host?:string;details?:{parameter_size?:string;quantization_level?:string}}
export interface Snapshot {timestamp:string;gpus:{id:string;name:string;memoryUsed:number|null;memoryTotal:number|null}[];ollamaOnline:boolean;ollamaError:string;inferenceOnline?:boolean;providers?:{id:string;name:string;endpoint:string;online:boolean;error?:string}[];models:Model[];running:Model[]}
export const errorText=(error:unknown)=>error instanceof Error?error.message:String(error);
export const workspace=<T>(method:string,...args:unknown[]):Promise<T>=>Call.ByName(`main.WorkspaceService.${method}`,...args);
export const comparison=<T>(method:string,...args:unknown[]):Promise<T>=>Call.ByName(`main.ComparisonService.${method}`,...args);
export const onComparison=(callback:(event:ComparisonEvent)=>void):(()=>void)=>Events.On('comparison:chunk',(event:{data:ComparisonEvent})=>callback(event.data));
export interface Document {id:string;name:string;kind:string;size:number;imported:string;text?:string;chunks?:string[]}
export interface SearchHit {documentID:string;name:string;chunk:number;text:string;score:number;label?:string}
export interface Artifact {id:string;title:string;kind:string;language:string;content:string;updated?:string}
export interface ComparisonResult {model:string;response:string;reasoning?:string;tokens:number;tokensPerSecond:number;seconds:number;error?:string;rateBasis?:string}
export const modelLabel=(name:string)=>name.replace(/^fastflowlm::/,'FLM · ');
export interface Comparison {id:string;prompt:string;created:string;results:ComparisonResult[]}
export interface ComparisonEvent {id:string;model?:string;index:number;text?:string;reasoning?:string;result?:ComparisonResult;complete:boolean;cancelled:boolean;saveWarning?:string}
export const acceptedDocuments='.txt,.md,.markdown,.csv,.json,.go,.py,.js,.jsx,.ts,.tsx,.html,.css,.yaml,.yml,.xml,.log,.sql,.rs,.c,.cpp,.h,.java,.sh,.ps1,.pdf,.docx';
export async function importDocument(file:File):Promise<Document>{
  if(file.size>10*1024*1024)throw new Error('Attachments must be 10 MiB or smaller.');
  const encoded=await new Promise<string>((resolve,reject)=>{const reader=new FileReader();reader.onload=()=>resolve(String(reader.result).split(',')[1]);reader.onerror=()=>reject(new Error('Could not read this file.'));reader.readAsDataURL(file);});
  return workspace<Document>('Import',file.name,encoded);
}
