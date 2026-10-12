"""Identify Java and Go servers independently of their compatible version bodies."""
import json, urllib.request

def identify(base, kind, timeout=2):
    with urllib.request.urlopen(base.rstrip('/')+'/__admin/version', timeout=timeout) as response:
        version=json.load(response).get('version')
        candidate=response.headers.get('X-BreakMock-Version')
        language=response.headers.get('X-BreakMock-Language','go' if candidate else 'java')
    if version!='4.0.0-beta.40':
        raise RuntimeError(f'{base}: wrong compatibility version {version!r}')
    if kind=='java' and candidate is not None:
        raise RuntimeError(f'{base}: reference is BreakMock, not Java WireMock')
    if kind=='go' and (not candidate or language!=kind):
        raise RuntimeError(f'{base}: candidate missing BreakMock identity header')
    return {'version':version,'implementation_version':candidate,'language':language}
