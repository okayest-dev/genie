import os
path = os.path.expanduser("~/.genie/plugins")
if os.path.exists(path):
    for item in os.listdir(path):
        full = os.path.join(path, item)
        print(f"{item} {'dir' if os.path.isdir(full) else 'file'}")
else:
    print("Path does not exist:", path)