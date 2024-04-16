import subprocess
import sys
import time
import datetime
# 定义第五个终端要执行的PowerShell命令

cnt = 0
for i in range(40):
    cnt+=1
    # 动态构建带有当前循环i值的PowerShell命令
    ps_command = f"""
        $headers = @{{ "Content-Type" = "application/json" }}
        $body = '{{"clientID":"ahnhwi","operation":"SendMes1 - {i}","timestamp":{i}}}'
        $response = Invoke-WebRequest -Uri "47.107.59.211:1110/req" -Method POST -Headers $headers -Body $body
        """
    ps_command2 = f"""
        $headers = @{{ "Content-Type" = "application/json" }}
        $body = '{{"clientID":"ahnhwi","operation":"SendMes2 - {i}","timestamp":{i}}}'
        $response = Invoke-WebRequest -Uri "114.55.130.178:1114/req" -Method POST -Headers $headers -Body $body
        """
    ps_command3 = f"""
        $headers = @{{ "Content-Type" = "application/json" }}
        $body = '{{"clientID":"ahnhwi","operation":"SendMes3 - {i}","timestamp":{i}}}'
        $response = Invoke-WebRequest -Uri "114.55.130.178:1118/req" -Method POST -Headers $headers -Body $body
        """
    subprocess.Popen(['powershell', '-Command', ps_command])
    subprocess.Popen(['powershell', '-Command', ps_command2])
    subprocess.Popen(['powershell', '-Command', ps_command3])
    if cnt % 20 == 0:
        #time.sleep(1)
        pass