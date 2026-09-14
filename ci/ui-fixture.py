#!/usr/bin/env python3
"""Local-only disposable UI fixture; writes readiness, stops on a .stop marker."""
import argparse
import datetime
import json
import os
from pathlib import Path
import secrets
import shutil
import sqlite3
import subprocess
import tempfile
import time
import urllib.error
import urllib.request


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--server', required=True)
    parser.add_argument('--dir', required=True)
    parser.add_argument('--port', type=int, default=9015)
    args = parser.parse_args()
    directory = Path(args.dir).resolve()
    directory.mkdir(parents=True, exist_ok=True)
    work = Path(tempfile.mkdtemp(prefix='ui-', dir=directory))
    env = os.environ.copy()
    env.update(VM_LISTEN=f'127.0.0.1:{args.port}', VM_DATA_DIR=str(work / 'data'), VM_JWT_SECRET=secrets.token_hex(32), VM_TZ='Asia/Shanghai')
    log = (work / 'server.log').open('w')
    process = subprocess.Popen([str(Path(args.server).resolve())], env=env, stdout=log, stderr=log,
                               creationflags=0x08000000 if os.name == 'nt' else 0)
    base = f'http://127.0.0.1:{args.port}'
    token = ''
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
    def api(method, path, body=None):
        req = urllib.request.Request(base+path, data=json.dumps(body).encode() if body is not None else None,
              method=method, headers={'Content-Type':'application/json','Authorization':'Bearer '+token})
        with opener.open(req, timeout=10) as response:
            raw = response.read()
            return json.loads(raw) if raw else None
    try:
        password = None
        for _ in range(150):
            assert process.poll() is None, 'UI server failed to start'
            for line in (work/'server.log').read_text(encoding='utf-8').splitlines():
                try:
                    row=json.loads(line)
                    if row.get('msg')=='initial admin password (printed only once)': password=row['password']
                except ValueError: pass
            if password: break
            time.sleep(.1)
        assert password
        token=api('POST','/api/auth/sign-in',{'username':'admin','password':password})['accessToken']
        # Public, disposable QA password; never changes the existing panel account.
        qa_password='Qa-local!2026-test-only'
        api('POST','/api/auth/password',{'oldPassword':password,'newPassword':qa_password})
        nodes=[]
        for name in ['新加坡 · UI验收','香港 · 中转目标']:
            nodes.append(api('POST','/api/servers',{'name':name,'public_host':'127.0.0.1','currency':'USD','price':5,'billing_cycle':'month','traffic_mode':'sum','traffic_limit':10**12,'traffic_reset_day':15})['server'])
        inbounds=[]
        for i,kind in enumerate(['shadowsocks','vless','hysteria2','tuic']):
            inbounds.append(api('POST',f'/api/servers/{nodes[0]["id"]}/inbounds',{'protocol':kind,'listen_port':24000+i})['inbound'])
        today=datetime.datetime.now(datetime.timezone(datetime.timedelta(hours=8))).date()
        users=[]
        for name,enabled,expiry in [('验收用户',True,str(today+datetime.timedelta(days=10))),('超额用户',True,None),('已到期用户',True,str(today-datetime.timedelta(days=1))),('手动停用用户',False,None)]:
            user=api('POST','/api/subscribers',{'name':name,'enabled':enabled,'traffic_limit':1024**3,'reset_day':15,'expire_at':expiry})['subscriber']
            api('PUT',f'/api/subscribers/{user["id"]}/assignments',{'inbound_ids':[x['id'] for x in inbounds]})
            users.append(user)
        # Layout-only fixtures for non-zero charts/status. These are not runtime evidence.
        with sqlite3.connect(work/'data'/'vm.db') as db:
            db.execute("UPDATE subscribers SET traffic_used=1073741824,auto_disabled='quota' WHERE id=?",(users[1]['id'],))
            db.execute("INSERT INTO subscriber_traffic(subscriber_id,server_id,period_start,up_bytes,down_bytes) VALUES(?,?,?,?,?)",(users[0]['id'],nodes[0]['id'],users[0]['period_start'],100*1024**2,200*1024**2))
            db.execute("UPDATE subscribers SET traffic_used=? WHERE id=?",(300*1024**2,users[0]['id']))
            for i in range(10):
                db.execute("INSERT INTO subscriber_traffic_daily(subscriber_id,date,up_bytes,down_bytes) VALUES(?,?,?,?)",(users[0]['id'],str(today-datetime.timedelta(days=i)),(i+1)*1024**2,(11-i)*1024**2))
        ready={'base':base,'work':str(work),'username':'admin','password':qa_password,'node_id':nodes[0]['id'],'subscriber_id':users[0]['id'],'pid':process.pid}
        (directory/'ui-ready.json').write_text(json.dumps(ready),encoding='utf-8')
        print(json.dumps({'ready':True,'base':base,'stop_file':str(work/'.stop')}),flush=True)
        while not (work/'.stop').exists() and process.poll() is None: time.sleep(.25)
    finally:
        if process.poll() is None:
            process.terminate()
            process.wait(timeout=15)
        log.close()
        assert work.parent==directory and work.name.startswith('ui-')
        shutil.rmtree(work)
        (directory/'ui-ready.json').unlink(missing_ok=True)


if __name__=='__main__': main()
