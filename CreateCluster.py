import subprocess
import time
import sys

# 检查命令行参数的数量
if len(sys.argv) < 4:
    print("Usage: python script.py <arg> <nodes_per_group> <nodeNum>")
    sys.exit(1)

arg = sys.argv[1]
nodes_per_group = int(sys.argv[2])
nodeNum = nodes_per_group

# 定义命令模板和数量
command_template = './app'
groups = ['N', 'M', 'P']

# 生成命令列表
commands = [(command_template, f'{group}{i}', group, str(nodeNum)) for group in groups for i in range(nodes_per_group)]

def run_commands(arg):
    print("Starting commands...")

    # 执行 go build 命令
    print("Building Go application...")
    subprocess.run(['go', 'build', '-o', 'app'])

    # 等待一段时间以确保编译完成
    print("Waiting for build to finish...")
    time.sleep(1)

    subprocess.run(['tmux', 'new-session', '-d', '-s', 'myPBFT'])

    # 根据提供的 arg 值过滤命令
    filtered_commands = [cmd for cmd in commands if cmd[2] == arg]

    # 遍历过滤后的命令列表
    for index, (exe, arg1, arg2, nodeNum) in enumerate(filtered_commands):
        window_name = f"app-{arg1}"
        subprocess.run(['tmux', 'new-window', '-t', f'myPBFT:{index + 1}', '-n', window_name])
        time.sleep(0.1)

        # 在每个命令的末尾添加 nodeNum
        tmux_command = f"tmux send-keys -t myPBFT:{index + 1} '{exe} {arg1} {arg2} {nodeNum}' C-m"
        subprocess.run(['bash', '-c', tmux_command])

    time.sleep(2)

def run_commands_MP():
    print("Starting commands...")

    # 执行 go build 命令
    print("Building Go application...")
    subprocess.run(['go', 'build', '-o', 'app'])

    # 等待一段时间以确保编译完成
    print("Waiting for build to finish...")
    time.sleep(1)

    subprocess.run(['tmux', 'new-session', '-d', '-s', 'myPBFT'])

    # 根据提供的 arg 值过滤命令
    filtered_commands = [cmd for cmd in commands if cmd[2] != "N"]

    # 遍历过滤后的命令列表
    for index, (exe, arg1, arg2, nodeNum) in enumerate(filtered_commands):
        window_name = f"app-{arg1}"
        subprocess.run(['tmux', 'new-window', '-t', f'myPBFT:{index + 1}', '-n', window_name])
        time.sleep(0.1)

        # 在每个命令的末尾添加 nodeNum
        tmux_command = f"tmux send-keys -t myPBFT:{index + 1} '{exe} {arg1} {arg2} {nodeNum}' C-m"
        subprocess.run(['bash', '-c', tmux_command])

    time.sleep(2)

if __name__ == "__main__":
    if arg == "N":
        run_commands(arg)
    else:
        run_commands_MP()
