import fs from 'node:fs';
import { sql, closePg } from '/opt/baarcha/app/landing/src/lib/pg.ts';
import { chatWrite } from '/opt/baarcha/app/landing/src/lib/chat/write.ts';
import { projectToolResult } from '/opt/baarcha/app/landing/src/lib/chat-store.ts';
import { registerSupervision } from '/opt/baarcha/app/landing/src/lib/projects/supervision-store.ts';
const root='/opt/baarcha/operations/scheduler-budget-20261013';
const receipt=JSON.parse(fs.readFileSync(root+'/resumed.json','utf8'));
const cid='cca824b09-d723-4ff0-9134-5c68dcdcd23f';
try {
 const db=sql();
 const [creation]=await db`SELECT * FROM project_creation WHERE owner_id=1 AND project_id=${receipt.projectId}`;
 if (creation?.phase!=='complete' || creation.state.task.id!==receipt.taskId) throw Error('Build state changed');
 const [message]=await db`SELECT * FROM chat_message WHERE id=30535592 AND waitlist_id=1 AND conversation_id=4567286`;
 if (!message || message.generated_project?.id!==receipt.projectId || message.generated_project.taskId) throw Error('Chat card changed');
 const supervised=await registerSupervision(1,{conversationId:cid,messageId:message.client_id},creation.state.project,receipt.taskId,creation.input.prompt);
 await chatWrite(1,db,async tx=>{
  const [current]=await tx`SELECT * FROM chat_message WHERE id=${message.id} AND waitlist_id=1 FOR UPDATE`;
  if (current.generated_project?.taskId || current.generated_project?.id!==receipt.projectId) throw Error('Chat changed during recovery');
  fs.writeFileSync(root+'/chat-before.PRIVATE.json',JSON.stringify(current,null,2),{mode:0o600,flag:'wx'});
  const generated={...current.generated_project,taskId:receipt.taskId,...(supervised?{supervised:true}:{})};
  await tx`UPDATE chat_message SET generated_project=${tx.json(generated)},status=NULL,updated_at=now() WHERE id=${message.id}`;
  await tx`UPDATE chat_message SET text='The sandbox has recovered and your saved build has resumed.',updated_at=now()
    WHERE id=30535997 AND waitlist_id=1 AND conversation_id=4567286 AND role='notice'
      AND text='The sandbox operation is awaiting reconciliation. Its saved data is retained.'`;
  await tx`UPDATE chat_tool_call SET result=${JSON.stringify(projectToolResult(generated))},ok=true,answered_at=now()
    WHERE message_id=${message.id} AND call_id=${generated.toolCallId} AND result IS NULL`;
  await tx`UPDATE chat_conversation SET updated_at=GREATEST(updated_at,now()) WHERE id=4567286 AND waitlist_id=1 AND client_id=${cid}`;
 });
 const result={...receipt,chatRestored:true,supervised};
 fs.writeFileSync(root+'/chat-restored.json',JSON.stringify(result,null,2),{mode:0o600,flag:'wx'});console.log(JSON.stringify(result));
} finally { await closePg(); }
