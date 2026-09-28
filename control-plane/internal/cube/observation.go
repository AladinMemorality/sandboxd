package cube

import (
 "bytes"
 "io"
 "net/http"
 "net/url"
 "context"
 "encoding/json"
 "errors"
 "time"
)

// One authoritative CubeMaster read supplies both runtime state/resources and
// placement. Unlike CubeAPI GET, it does not enumerate the worker for a summary.
// Never cache this observation across operations or follow guest-provided URLs.
func (c *Client) getPlaced(ctx context.Context,id string)(*Sandbox,error){
 if c.observation!=nil && c.admission!=nil && c.admission.config.NodeID!="" {
  out,err:=c.observation(ctx,id)
  if err!=nil{return nil,err}
  if out.ClientID!=c.admission.config.NodeID{return nil,ErrAdmissionPending}
  return out,nil
 }
 out,err:=c.getRaw(ctx,id)
 if err==nil && c.admission!=nil && c.admission.config.NodeID!="" && c.placement(ctx,id,c.admission.config.NodeID)!=nil{return nil,ErrAdmissionPending}
 return out,err
}

func decodeMasterObservation(raw []byte,id string)(*Sandbox,error){
 var result struct {
  Ret struct {Code *int `json:"ret_code"`} `json:"ret"`
  Data []struct {
   ID string `json:"sandbox_id"`
   Host string `json:"host_id"`
   Template string `json:"template_id"`
   Status *int `json:"status"`
   EndAt int64 `json:"end_at"`
   Labels map[string]string `json:"labels"`
   Containers []struct {
    ID string `json:"container_id"`
    CPU int `json:"cpu_milli"`
    Memory int `json:"memory_mib"`
   } `json:"containers"`
  } `json:"data"`
 }
 if json.Unmarshal(raw,&result)!=nil || result.Ret.Code==nil{return nil,errors.New("invalid Cube runtime observation")}
 if *result.Ret.Code==130404 || (*result.Ret.Code==200 && len(result.Data)==0){return nil,&APIError{Operation:"get",StatusCode:404}}
 if *result.Ret.Code!=200 || len(result.Data)!=1{return nil,errors.New("invalid Cube runtime observation")}
 row:=result.Data[0]
 if row.ID!=id || row.Host=="" || row.Status==nil{return nil,errors.New("invalid Cube runtime identity")}
 out:=&Sandbox{SandboxID:row.ID,TemplateID:row.Template,ClientID:row.Host,Metadata:row.Labels}
 switch *row.Status {case 1:out.State="running";case 4:out.State="pausing";case 5:out.State="paused";default:out.State="unknown"}
 if err:=validateSandbox(out,id);err!=nil{return nil,err}
 for _,container:=range row.Containers {if container.ID==id {
  if out.CPUCount!=0 || container.CPU<=0 || container.CPU%1000!=0 || container.Memory<=0{return nil,errors.New("invalid Cube runtime resources")}
  out.CPUCount=container.CPU/1000;out.MemoryMB=container.Memory
 }}
 if out.CPUCount==0{return nil,errors.New("missing Cube runtime resource observation")}
 if row.EndAt>0 {end:=time.UnixMilli(row.EndAt);out.EndAt=&end}
 return out,nil
}

// Admission has already read this paused runtime. Use CubeMaster's same resume
// operation without CubeAPI repeating the before/after reads. The caller still
// verifies state/resources/placement afterward and retains uncertain admission.
func(c *Client)nativeResume(origin *url.URL,instance string)func(context.Context,string,ConnectRequest)error{
 return func(ctx context.Context,id string,in ConnectRequest)error{
  if validateID(id)!=nil{return ErrAdmissionUnknown}
  requestID,err:=admissionToken();if err!=nil{return err}
  body,_:=json.Marshal(map[string]any{"requestID":requestID,"sandbox_id":id,"instance_type":instance,"action":"resume","timeout":in.TimeoutSeconds})
  target:=*origin;target.Path="/cube/sandbox/update"
  bounded,cancel:=context.WithTimeout(ctx,lifecycleTimeout);defer cancel()
  req,err:=http.NewRequestWithContext(bounded,http.MethodPost,target.String(),bytes.NewReader(body));if err!=nil{return err}
  req.Header.Set("Content-Type","application/json")
  response,err:=c.http.Do(req);if err!=nil{return errors.New("Cube native resume unavailable")};defer response.Body.Close()
  raw,err:=io.ReadAll(io.LimitReader(response.Body,maxResponseBytes+1));if err!=nil||len(raw)>maxResponseBytes||response.StatusCode!=200{return errors.New("Cube native resume rejected")}
  var result struct{Ret struct{Code *int `json:"ret_code"`} `json:"ret"`}
  if json.Unmarshal(raw,&result)!=nil||result.Ret.Code==nil||*result.Ret.Code!=200{return errors.New("Cube native resume unconfirmed")}
  return nil
 }
}
