//go:build darwin

#include "textflag.h"

TEXT libc_ioctl_trampoline<>(SB),NOSPLIT,$0-0
	JMP	libc_ioctl(SB)

GLOBL	·libc_ioctl_trampoline_addr(SB), RODATA, $8
DATA	·libc_ioctl_trampoline_addr(SB)/8, $libc_ioctl_trampoline<>(SB)
