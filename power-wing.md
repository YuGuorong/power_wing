This is project to for vairiants of power meter & usb power controller, the program runing in both windows/Ubuntu OS
1. Webview UI, easy customization, Project name is "PowerWing"
2. Plug-in adaptable, vairiant equipment with differnet protocol by USB/HID/UART communications
3. `Support voice command by PC OS micphone and speaker(Major in widnows)
4. Support command line to control USB/power out enable/disable and configuations
5. Page mode, each page for one power supply , may a plug-in device, may have own UI 
6. power supply support :  
      get/set output voltage ,
	  get/set output current,
	  get/set output enable
	  get/set OCP/OVP limitation
7. Support mouse only configuations ,means voltage and current can set number by a control
8. Support control from windows tray  bar, currnet power supply 

Power supply 1:Name:SPM3051 , UART,    protocol see @"SPM3051-CMD.txt"
	
Power supply 2:Name:PD_Pocket, UART ,  protocol see @"PD_Pocket API.txt"
USB Hub :Name USB-Slim , support 4 USB Ports,  controll on/off by UART,  protocol see @"Usb_slim-api.txt"
