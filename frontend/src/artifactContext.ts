import {createContext} from 'react';
import type {Artifact} from './bridge';
export const ArtifactContext=createContext<((artifact:Artifact)=>void)|null>(null);
export const DocumentContext=createContext<((id:string)=>void)|null>(null);
