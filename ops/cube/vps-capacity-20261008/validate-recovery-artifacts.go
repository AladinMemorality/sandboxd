package main

import (
 "encoding/json"
 "fmt"
 "os"
 "path/filepath"
 "reflect"
 "sort"
 "github.com/tastyeffectco/sandboxd/control-plane/internal/runtime"
)
func check(dir string)error {
 raw,e:=os.ReadFile(filepath.Join(dir,"export-result.PRIVATE.json"));if e!=nil{return e}
 var receipt struct {Manifest runtime.HomeManifest `json:"home_manifest"`;Tasks []string `json:"task_ids"`}
 if e=json.Unmarshal(raw,&receipt);e!=nil{return e}
 app,e:=os.ReadFile(filepath.Join(dir,"workspace.zip"));if e!=nil{return e}
 if _,e=runtime.PrivateWorkspaceDigest(app);e!=nil{return fmt.Errorf("workspace: %w",e)}
 f,e:=os.Open(filepath.Join(dir,"home.zip"));if e!=nil{return e};defer f.Close();st,e:=f.Stat();if e!=nil{return e}
 if _,e=runtime.PrivateHomeDigest(receipt.Manifest,f,st.Size());e!=nil{return fmt.Errorf("home: %w",e)}
 history,e:=os.ReadFile(filepath.Join(dir,"history.zip"));if e!=nil{return e}
 ids,e:=runtime.PrivateTaskHistoryIDs(history);if e!=nil{return fmt.Errorf("history: %w",e)}
 sort.Strings(ids);sort.Strings(receipt.Tasks)
 if len(ids)!=len(receipt.Tasks)||(len(ids)>0&&!reflect.DeepEqual(ids,receipt.Tasks)){return fmt.Errorf("history: task inventory differs")}
 return nil
}
func main(){err:=check(os.Args[1]);out:=map[string]any{"valid":err==nil};if err!=nil{out["error"]=err.Error()};json.NewEncoder(os.Stdout).Encode(out);if err!=nil{os.Exit(1)}}
