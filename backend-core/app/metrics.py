"""Recolección de datos crudos de la máquina — el equivalente en Python del
paquete Go `internal/sysinfo` que usa el CLI (`asterion local info/stats`).
Existen dos porque son dos procesos distintos con dos propósitos distintos:
sysinfo (Go) alimenta al CLI y al agente que empuja métricas a Asterion
Cloud; este módulo alimenta al dashboard local embebido (frontend-core)
servido por backend-core. Mismo principio en los dos: nunca calcular costo
acá — eso vive en app/pricing.py, aparte.
"""

import platform
import socket
import time
from pathlib import Path

import psutil

_VM_MARKERS = ["QEMU", "VMware", "VirtualBox", "KVM", "Xen", "Microsoft Corporation", "Google", "Amazon EC2"]


def detect_virtualization() -> str:
    if Path("/.dockerenv").exists():
        return "container"
    try:
        cgroup = Path("/proc/1/cgroup").read_text()
        if any(marker in cgroup for marker in ("docker", "kubepods", "containerd")):
            return "container"
    except OSError:
        pass

    for dmi_path in ("/sys/class/dmi/id/product_name", "/sys/class/dmi/id/sys_vendor"):
        try:
            value = Path(dmi_path).read_text().strip()
        except OSError:
            continue
        if any(marker in value for marker in _VM_MARKERS):
            return "virtual-machine"
        if value:
            return "physical"
    return "unknown"


def gather_info() -> dict:
    disk = psutil.disk_usage("/")
    mem = psutil.virtual_memory()
    return {
        "hostname": socket.gethostname(),
        "os": platform.system().lower(),
        "architecture": platform.machine(),
        "kernel_version": platform.release(),
        "cpu_model": platform.processor() or None,
        "cpu_cores": psutil.cpu_count(logical=True) or 0,
        "ram_total_gb": round(mem.total / 1024**3, 2),
        "disk_total_gb": round(disk.total / 1024**3, 2),
        "virtualization": detect_virtualization(),
    }


def collect_snapshot() -> dict:
    disk = psutil.disk_usage("/")
    mem = psutil.virtual_memory()
    net = psutil.net_io_counters(pernic=True)
    bytes_recv = sum(v.bytes_recv for k, v in net.items() if k != "lo")
    bytes_sent = sum(v.bytes_sent for k, v in net.items() if k != "lo")

    return {
        "cpu_percent": psutil.cpu_percent(interval=0.2),
        "ram_used_gb": round((mem.total - mem.available) / 1024**3, 2),
        "ram_total_gb": round(mem.total / 1024**3, 2),
        "disk_used_gb": round(disk.used / 1024**3, 2),
        "disk_total_gb": round(disk.total / 1024**3, 2),
        "network_in_gb": round(bytes_recv / 1024**3, 4),
        "network_out_gb": round(bytes_sent / 1024**3, 4),
        "taken_at": time.time(),
    }
