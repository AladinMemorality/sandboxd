WITH counts AS (
 SELECT 'waitlist' AS name,count(*) AS count,
 encode(sha256(convert_to(coalesce(string_agg(to_jsonb(t)::text,E'\n' ORDER BY id),''),'UTF8')),'hex') AS sha256 FROM waitlist t
 UNION ALL SELECT 'platform_session',count(*),encode(sha256(convert_to(coalesce(string_agg(to_jsonb(t)::text,E'\n' ORDER BY token_hash),''),'UTF8')),'hex') FROM platform_session t
 UNION ALL SELECT 'published_app',count(*),encode(sha256(convert_to(coalesce(string_agg(to_jsonb(t)::text,E'\n' ORDER BY id),''),'UTF8')),'hex') FROM published_app t
 UNION ALL SELECT 'upload',count(*),encode(sha256(convert_to(coalesce(string_agg(to_jsonb(t)::text,E'\n' ORDER BY id),''),'UTF8')),'hex') FROM upload t
 UNION ALL SELECT 'schema_migrations',count(*),encode(sha256(convert_to(coalesce(string_agg(to_jsonb(t)::text,E'\n' ORDER BY version),''),'UTF8')),'hex') FROM schema_migrations t
)
SELECT jsonb_build_object(
 'tables',(SELECT jsonb_object_agg(name,jsonb_build_object('count',count,'sha256',sha256)) FROM counts),
 'canary',(SELECT jsonb_build_object('project_id',a.project_id,'owner',a.owner_waitlist_id,'visibility',a.visibility,'cover_id',a.cover_upload_id,'upload_owner',u.waitlist_id,'upload_project',u.project_id,'upload_sha256',u.sha256,'upload_bytes',u.bytes,'upload_storage',u.storage,'upload_deleted',u.deleted_at)
  FROM published_app a JOIN upload u ON u.id=a.cover_upload_id WHERE a.project_id='01M3CZB4HXT2Y8HP8CEY75PCWY'),
 'fixture_owners',(SELECT count(*) FROM waitlist WHERE id IN (103,104))
) AS proof;
