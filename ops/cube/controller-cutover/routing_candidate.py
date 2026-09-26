"""Pure reviewed transformation; no network calls or file installation."""
import copy


def extend_alias(old_online, live, drain, offline, hostname):
    def routes(value):
        return value['apps']['http']['servers']['srv0']['routes']
    old, current = routes(old_online), routes(live)
    added = [r for r in current if r not in old]
    if len(added) != 1 or [r for r in current if r not in added] != old:
        raise ValueError('requires exactly one added alias; existing route order/content unchanged')
    alias = added[0]
    if alias.get('match') != [{'host': [hostname]}] or [h.get('handler') for h in alias.get('handle', [])] != ['reverse_proxy']:
        raise ValueError('unexpected added alias match/handler')
    stripped = copy.deepcopy(live)
    routes(stripped).remove(alias)
    if stripped != old_online:
        raise ValueError('unreviewed non-route Caddy change')
    previews = [r for r in routes(offline) if any('*.preview.65.108.225.153.sslip.io' in m.get('host', []) for m in r.get('match', []))]
    if len(previews) != 1:
        raise ValueError('reviewed wildcard preview fence missing')
    def has_503(value):
        if isinstance(value, dict):
            return (value.get('handler') == 'static_response' and str(value.get('status_code')) == '503') or any(has_503(v) for v in value.values())
        return isinstance(value, list) and any(has_503(v) for v in value)
    if not has_503(previews[0]['handle']):
        raise ValueError('reviewed preview fence is not a503 response')
    out_drain, out_offline = copy.deepcopy(drain), copy.deepcopy(offline)
    fenced = copy.deepcopy(alias)
    fenced['handle'] = copy.deepcopy(previews[0]['handle'])
    position = current.index(alias)
    routes(out_drain).insert(position, copy.deepcopy(alias))
    routes(out_offline).insert(position, fenced)
    return out_drain, out_offline
