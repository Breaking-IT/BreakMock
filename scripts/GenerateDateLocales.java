// Generates observable locale data from the reference JDK; no Java runtime is used by BreakMock.
import com.github.tomakehurst.wiremock.common.Json;
import java.time.*;
import java.time.chrono.IsoChronology;
import java.time.format.*;
import java.util.*;
import java.nio.file.*;
import java.util.zip.GZIPOutputStream;
class GenerateDateLocales {
 public static void main(String[] args)throws Exception {
 Map<String,Object> locales=new TreeMap<>();
 for(Locale locale:Locale.getAvailableLocales()) {
 Map<String,Object> data=new LinkedHashMap<>();Map<String,String> patterns=new LinkedHashMap<>();
 for(FormatStyle date:FormatStyle.values()) {
 String name=date.name().toLowerCase(Locale.ROOT);
 patterns.put(name,DateTimeFormatterBuilder.getLocalizedDateTimePattern(date,null,IsoChronology.INSTANCE,locale));
 for(FormatStyle time:FormatStyle.values())patterns.put(name+"/"+time.name().toLowerCase(Locale.ROOT),DateTimeFormatterBuilder.getLocalizedDateTimePattern(date,time,IsoChronology.INSTANCE,locale));
 }
 data.put("patterns",patterns);
 Map<String,List<String>> symbols=new LinkedHashMap<>();
 for(String token:List.of("MMM","MMMM","MMMMM","LLL","LLLL","LLLLL")) {
 List<String> values=new ArrayList<>();for(int month=1;month<=12;month++)values.add(DateTimeFormatter.ofPattern(token,locale).format(LocalDate.of(2000,month,1)));symbols.put(token,values);
 }
 for(String token:List.of("EEE","EEEE","EEEEE")) {
 List<String> values=new ArrayList<>();for(int day=0;day<7;day++)values.add(DateTimeFormatter.ofPattern(token,locale).format(LocalDate.of(2024,1,1).plusDays(day)));symbols.put(token,values);
 }
 for(String token:List.of("a","B")) {List<String> values=new ArrayList<>();for(int hour=0;hour<24;hour++){values.add(DateTimeFormatter.ofPattern(token,locale).format(LocalTime.of(hour,0)));values.add(DateTimeFormatter.ofPattern(token,locale).format(LocalTime.of(hour,30)));};symbols.put(token,values);}
 symbols.put("G",List.of(DateTimeFormatter.ofPattern("G",locale).format(LocalDate.of(2000,1,1))));
 data.put("symbols",symbols);locales.put(locale.toLanguageTag(),data);
 }
 Map<String,Object> output=new LinkedHashMap<>();output.put("javaVersion",System.getProperty("java.version"));output.put("javaVendor",System.getProperty("java.vendor"));output.put("locales",locales);
 try(var stream=new GZIPOutputStream(Files.newOutputStream(Path.of(args[0])))){stream.write(Json.write(output).getBytes(java.nio.charset.StandardCharsets.UTF_8));}
 System.out.println("Generated "+locales.size()+" locales from "+System.getProperty("java.version"));
 }
}
