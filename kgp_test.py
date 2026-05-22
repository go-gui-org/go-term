import base64, sys

path = "../go-charts/screenshot.png"
data = open(path, "rb").read()
b64 = base64.b64encode(data).decode()

chunk_size = 4096
chunks = [b64[i:i+chunk_size] for i in range(0, len(b64), chunk_size)]

for idx, chunk in enumerate(chunks):
    more = "1" if idx < len(chunks) - 1 else "0"
    sys.stdout.write("\x1b_Ga=T,f=100,q=1,m=" + more + ";" + chunk + "\x1b\\")

sys.stdout.flush()
