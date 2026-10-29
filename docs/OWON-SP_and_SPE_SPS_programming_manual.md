# OWON-SP_and_SPE_SPS_programming_manual.pdf
## Metadata
- PDFFormatVersion=1.7
- Language=zh-CN
- IsLinearized=false
- IsAcroFormPresent=false
- IsXFAPresent=false
- IsCollectionPresent=false
- IsSignaturesPresent=false
- Title=目录
- Author=OWON
- Creator=Microsoft® Word 2019
- CreationDate=D:20211220094549+08'00'
- ModDate=D:20211220094549+08'00'
- Producer=Microsoft® Word 2019
- pdf:producer=Microsoft® Word 2019
- dc:title=目录
- dc:creator=OWON
- xmp:creatortool=Microsoft® Word 2019
- xmp:createdate=2021-12-20T09:45:49+08:00
- xmp:modifydate=2021-12-20T09:45:49+08:00
- xmpmm:documentid=uuid:04481C53-B92E-4638-B3F7-A2A12C0F9004
- xmpmm:instanceid=uuid:04481C53-B92E-4638-B3F7-A2A12C0F9004


## Contents
### Page 1

Single ChannelDC Power Supply


ProgrammingManual


www.owon.com




### Page 2




### Page 3
# Introduction to the SCPI Language

## Syntax


SCPI commands present a hierarchical tree structure and contain multiple sub-systems, each of
which is made up of a root keyword and one or more sub-keywords. The command string
usually starts with ":", the keywords are separated by ":" and are followed by the parameter
settings available, "?" is added at the end of the command string to indicate query and the
command and parameter are separated by "space".


For example,


VOLTage:LIMit <value>


VOLTage:LIMit?


VOLTageis the root keyword of the command.LIMitisthe second-levelkeywords.Thecommand string starts with ":" which separates the multiple-level keywords.<value>represents parameters available for setting, "?" represents query and the commandVOLTage:LIMitand the parameter<value>are separated by "space".

## Syntax Rules


SCPI language itself defines a group of sub-system keywords, and at the same time allows usersto add or reduce keywords. Those keywords can be some meaningful English words and are easyto remember, which are called mnemonics. Mnemonic has longand short types. The short arethe abbreviation of the long.


- ➢Rule to format mnemonics:

- 1)If the letter numberof an English word is less than or equal to 4, then the word itself can bethe mnemonic.(such as"Free"can be"FREE")

- 2)If the letter number of an English word exceeds 4, then thefirst four letters will be themnemonic.(such as"Frequency"can be"FREQ")

- 3)If the forth letter is vowel, then mnemonic uses the former three letters. Vowels consists ofa, e, i, o, and u.(such as"Power"can be"POW")

- 4)If itis not a word but a sentence, then use the first letters of the former words and thewhole of the last word.(such as"Input Voltage"can be"IVOLtage")



- ➢Usage of symbols

- 1)Space



The space is used to separate command and parameter.


- 2)Colon :





### Page 4

If the colon isin front of the first character,it means the following is Root Command. When thecolon is set between two keywords, then it means moving from the current level to the nextlevel.


- 3)*asterisk



The commands start with asterisk are named Common Command, which is used to executeIEEE488.2 common commands.


- 4)Braces{}



The parameters enclosed in the braces are optional and are usually separated by the vertical
bar "|". When using this command, one of the parameters must be selected.


- 5)Vertical Bar|



The vertical bar is used to separate multiple parameters and one of the parameters must be
selected when using the command.


- 6)Triangle Brackets< >



The parameter enclosed in the triangle brackets must be replaced by an effective value.


- 7)SquareBrackets[]



The content (command keyword) enclosed in the square brackets can be omitted. When the
parameter is omitted, the instrument will set the parameter to its default.


- ➢Parameter Type

- 1)Real Number



Commands that require numeric parameters accept all commonly used decimal
representations of numbers including optional signs, decimal points, and scientific notation.
Special values for numeric parameters such as MIN, MAX and DEF are also accepted.


- 2)Discrete



The parameter should be one of the values listed.


- 3)Integer



Unless otherwise noted, the parameter can be any integer (NR1 format) within the
effective value range. Note that, do not set the parameter to a decimal, otherwise errors
will occur.


- 4)Bool



The parameter could be "OFF", "ON". For example,


OUTPut {0|1|ON|OFF}

## Command Abbreviation


Each SCPI command can bewritten mixed with uppercase and lowercase according to thesyntax rules, andthe capital letter part is just the abbreviation of the command. If abbreviationis used, all the capital letters in the command must be written completely. For parameters withunits, please refer to the detail parameter specifications in the sub-system.




### Page 5

Example：


MEASure[:SCALar]:VOLTage


Abbreviation Below：


MEAS:VOLT

## Contact Us


If you have any problem or requirement when using our products, please contactOWON.


Service & Support Hotline:+86-596-2130430


E-mail:[info@owon.com.cn](mailto:info@owon.com.cn)


Website：www.owon.com

## Third-partyAPI


TheSCPIprotocolcommunicationofthisinstrumentcanuseUSB,serialportorLANinterface.Runthesoftwareonthecomputer.Select"CommandLine"underthe"Transfer"menutoopentheSCPIcommandcontrolwindow,andthenyoucansendSCPIcommandstocontrolthepowersupply.




### Page 6
# IEEE488.2Common Commands

### *IDN?


Return the ID character string of the instrument.


Description


The query returns the ID character string of the instrument.


Return Format


OWON,<model>,<serial number>,FV:X.XX.XX<model>：the model number of the instrument.<serial number>：the serial number of the instrument.FV:X.XX.XX：the software version of the instrument.


Example


OWON,P4305,1715040,FV:V1.0.2

### *RST


Syntax


*RST


Description


Resets the oscilloscope to the factory default settings.


Example


Resets the oscilloscope to the factory default settings.


*RST




### Page 7
# SCPICommands

## MeasurementCommands


Measurement commands query the voltage, current and power measured on the output
terminal of the channels.

### MEASure:VOLTage?


Syntax


MEASure:VOLTage?


Description


Query the voltage measured on the output terminal of the channel.


Parameter


(none)


Example


The voltage measured on the output terminal of the channel is 1V.


MEAS:VOLT?


Returns


1.000

### MEASure:CURRent?


Syntax


MEASure:CURRent?


Description


Query the current measured on the output terminal of the channel.


Parameter


(none)


Example


The current measured on the output terminal of the channel is 1A.




### Page 8

MEAS:CURR?


Returns


1.000

### MEASure:POWer?


Syntax


MEASure:POWer?


Description


Query the power measured on the output terminal of the channel.


Parameter


(none)


Example


The power measured on the output terminal of the channel is 1W.


MEAS:POW?


Returns


1.000

### MEASure[:SCALar]:ALL[:DC]?


Syntax


MEASure:ALL?


Description


This command is used to query the voltage, current and power at the output of the currently
selected channel.


Parameter


(none)


Example


MEAS[:ALL]?


1.000 2.000 2.000


The voltage at the output terminal of the currently selected channel is 1V, the output current
is 2A, and the output power is 2W.

### MEASure[:SCALar]:ALL[:DC]:INFO?


Syntax




### Page 9

MEASure:ALL:INFO?


Description


This command is used to query the voltage, current, power and running status of the output t
erminal of the currently selected channel.


Parameter


(none)


Example


MEAS:ALL:INFO?


2.000 5.000 10.000 NR1 NR2 NR3 NR4


The voltage at the output terminal of the currently selected channel is 2V, the output
current is 5A, and the output power is 10W.


NR1: Bool, output overvoltage: 1|ON means fault, 0|OFF means normal;


NR2: Bool, output overcurrent: 1|ON means fault, 0|OFF means normal;


NR3: Bool, output over temperature: 1|ON means fault, 0|OFF means normal;


NR4:Integer , operating mode, 0 means standby mode, 1 means CV mode, 2 means CC
mode, 3 means failure mode.

## Output Setup Commands


The Output setup commands are used to enable or disable the channel output, switch the
output mode, as well as set the output value and limit value.

### OUTPut


Syntax


OUTPut {0|1|ON|OFF}


OUTPut?


Description


Controls on/off of power supply output.


Parameter


Example


- (1) The command below enables the channel output.



OUTP ON


- (2)The command belowqueries if thechannelis on.If thechannels is on, return 1, otherwisereturn 0.



OUTP?




### Page 10

returns


1

### CURRent


Syntax


CURRent <value>


CURRent?


Description


Setsand queriesthe currentofthe channel.


Parameter


value:Currentsettingvalue


Example


- (1)Thecommand belowsetsthecurrentof the channelto 1A.



CURR 1


- (2)The command belowqueriesthecurrentsetting valueofthe channel.



CURR?


Returns


1.000

### CURRent:LIMit


Syntax


CURRent:LIMit <value>


CURRent:LIMit?


Description


Setsand queriestheovercurrent protection (OCP) valueofthe channel.


Parameter


value:Overcurrent protection (OCP) value


Example


- (1)The command belowsetstheovercurrent protection (OCP) valueof the channelto 1A.



CURR:LIM 1


- (2)The command belowqueriestheovercurrent protection (OCP) valueofthe channel.





### Page 11

CURR:LIM?


Returns


1.000

### VOLTage


Syntax


VOLTage <value>


VOLTage?


Description


Setsand queriesthevoltageofthe channel.


Parameter


value:Voltagesetting value


Example


- (1)The command belowsetsthevoltageof the channelto 1V.



VOLT 1


- (2)The command belowqueriesthevoltagesetting valueofthe channel.



VOLT?


Returns


1.000

### VOLTage:LIMit


Syntax


VOLTage:LIMit <value>


VOLTage:LIMit?


Description


Setsand queries theovervoltageprotection (OVP) valueofthe channel.


Parameter


value:Overvoltage protection (OVP)value


Example


- (1)The command belowsetstheovervoltage protection (OVP)valueof the channelto 1V.



VOLT:LIM 1




### Page 12

- (2)The command belowqueriestheovervoltage protection (OVP)valueofthe channel.



VOLT:LIM?


Returns


1.000

## SystemcontrolCommands

### SYSTem:LOCal


Syntax


SYSTem:LOCal


Description


Thiscommandisusedtosetthepowersupplytothelocaloperatingstate.


Example


SYST:LOC

### SYSTem:REMote


Syntax


SYSTem:REMote


Description


Thiscommandisusedtosetthepowersupplytoremoteoperationstatus.


Example


SYST:REM


