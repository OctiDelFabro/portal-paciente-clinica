import {useEffect,useState} from 'react';
import {pacientes} from '../../api/endpoints';
export function usePatientNames(ids:string[]){const [names,setNames]=useState<Record<string,string>>({});const key=[...new Set(ids)].sort().join(',');useEffect(()=>{let alive=true;Promise.all((key?key.split(','):[]).map(id=>pacientes.contact(id).then(p=>[id,p.fullName] as const).catch(()=>[id,id] as const))).then(pairs=>{if(alive)setNames(Object.fromEntries(pairs))});return()=>{alive=false}},[key]);return names}
