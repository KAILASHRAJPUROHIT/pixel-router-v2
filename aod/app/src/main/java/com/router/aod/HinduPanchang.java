package com.router.aod;

import java.util.Calendar;
import java.util.TimeZone;

/**
 * Computes today's Panchang (Tithi, Paksha, Nakshatra) entirely offline
 * from the current date/time - no network dependency, unlike the router
 * stats which come from the local dashboard poll.
 *
 * Uses low-precision solar/lunar longitude formulas (Meeus, "Astronomical
 * Algorithms", truncated series - accurate to roughly a degree). That's
 * plenty for Tithi/Nakshatra, which only change at fixed longitude
 * thresholds; the one edge case is the exact hour a Tithi/Nakshatra
 * turns over, where this can be off by up to an hour or so.
 */
public final class HinduPanchang {

    private static final String[] TITHI_NAMES = {
            "Pratipada", "Dwitiya", "Tritiya", "Chaturthi", "Panchami",
            "Shashthi", "Saptami", "Ashtami", "Navami", "Dashami",
            "Ekadashi", "Dwadashi", "Trayodashi", "Chaturdashi"
    };

    private static final String[] NAKSHATRA_NAMES = {
            "Ashwini", "Bharani", "Krittika", "Rohini", "Mrigashira", "Ardra",
            "Punarvasu", "Pushya", "Ashlesha", "Magha", "Purva Phalguni", "Uttara Phalguni",
            "Hasta", "Chitra", "Swati", "Vishakha", "Anuradha", "Jyeshtha",
            "Mula", "Purva Ashadha", "Uttara Ashadha", "Shravana", "Dhanishta", "Shatabhisha",
            "Purva Bhadrapada", "Uttara Bhadrapada", "Revati"
    };

    public static final class Result {
        public final String tithi;
        public final String paksha;
        public final String nakshatra;

        Result(String tithi, String paksha, String nakshatra) {
            this.tithi = tithi;
            this.paksha = paksha;
            this.nakshatra = nakshatra;
        }
    }

    private HinduPanchang() {
    }

    public static Result compute(Calendar now) {
        double jd = julianDay(now);
        double t = (jd - 2451545.0) / 36525.0;

        double sunLon = sunEclipticLongitude(t);
        double moonLon = moonEclipticLongitude(t);

        double tithiAngle = norm360(moonLon - sunLon);
        int tithiIndex = (int) Math.floor(tithiAngle / 12.0); // 0..29
        boolean shuklaPaksha = tithiIndex < 15;
        int tithiInPaksha = shuklaPaksha ? tithiIndex : tithiIndex - 15; // 0..14
        String tithiName = tithiInPaksha == 14
                ? (shuklaPaksha ? "Purnima" : "Amavasya")
                : TITHI_NAMES[tithiInPaksha];

        int nakshatraIndex = (int) Math.floor(norm360(moonLon) / (360.0 / 27.0));
        nakshatraIndex = Math.max(0, Math.min(26, nakshatraIndex));

        return new Result(tithiName, shuklaPaksha ? "Shukla" : "Krishna", NAKSHATRA_NAMES[nakshatraIndex]);
    }

    // Fixed location - this router is stationary, matches the same
    // coordinates already used for weather (Boisar, Maharashtra).
    private static final double LOCATION_LAT = 19.8036;
    private static final double LOCATION_LON = 72.7560;

    // Weekday -> which of the 8 equal daylight segments (1-8, counting
    // from sunrise) each inauspicious period falls in. Standard tables
    // used by most Panchang calculators; index 1=Sunday..7=Saturday to
    // match Calendar.DAY_OF_WEEK.
    private static final int[] RAHU_KAAL_SEGMENT      = {0, 8, 2, 7, 5, 6, 4, 3}; // [Cal.DAY_OF_WEEK]
    private static final int[] YAMAGANDA_KAAL_SEGMENT = {0, 5, 4, 3, 2, 1, 7, 6};
    private static final int[] GULIKA_KAAL_SEGMENT    = {0, 7, 6, 5, 4, 3, 2, 1};

    /**
     * Which named time-window is active right now, e.g. "Rahu Kaal" or
     * "Abhijit Muhurat" - null if none of the specifically-computed
     * periods currently apply (the day isn't wall-to-wall named
     * periods; most of it has no special designation).
     */
    public static String currentPeriod(Calendar now) {
        double[] sunTimes = sunriseSunsetHours(now, LOCATION_LAT, LOCATION_LON);
        double sunrise = sunTimes[0];
        double sunset = sunTimes[1];
        if (sunset <= sunrise) return null; // polar edge case / calc failure - just skip

        double nowHours = now.get(Calendar.HOUR_OF_DAY) + now.get(Calendar.MINUTE) / 60.0
                + now.get(Calendar.SECOND) / 3600.0;
        if (nowHours < sunrise || nowHours > sunset) return null; // only defined during daylight

        double daylightHrs = sunset - sunrise;
        double segmentLen = daylightHrs / 8.0;
        int segment = (int) Math.floor((nowHours - sunrise) / segmentLen) + 1; // 1..8
        segment = Math.max(1, Math.min(8, segment));

        int dow = now.get(Calendar.DAY_OF_WEEK); // 1=Sunday..7=Saturday

        // Abhijit Muhurat: 8th of 15 equal muhurats spanning the day -
        // i.e. centered on solar midday, roughly the day's most
        // auspicious moment regardless of weekday.
        double muhurtaLen = daylightHrs / 15.0;
        double solarNoon = (sunrise + sunset) / 2.0;
        if (nowHours >= solarNoon - muhurtaLen / 2.0 && nowHours <= solarNoon + muhurtaLen / 2.0) {
            return "Abhijit Muhurat";
        }

        if (segment == RAHU_KAAL_SEGMENT[dow]) return "Rahu Kaal";
        if (segment == YAMAGANDA_KAAL_SEGMENT[dow]) return "Yamaganda Kaal";
        if (segment == GULIKA_KAAL_SEGMENT[dow]) return "Gulika Kaal";
        return null;
    }

    /**
     * Sunrise/sunset in local decimal hours (e.g. 6.25 = 6:15am), using
     * the standard NOAA/Edward-Williams sunrise equation - accurate to
     * a minute or two, which is plenty for an 8-way day split.
     * Returns {sunrise, sunset}; {-1,-1} if the calculation fails.
     */
    private static double[] sunriseSunsetHours(Calendar now, double lat, double lon) {
        Calendar utc = (Calendar) now.clone();
        int dayOfYear = utc.get(Calendar.DAY_OF_YEAR);
        double lngHour = lon / 15.0;
        double utcOffsetHours = now.get(Calendar.ZONE_OFFSET) / 3600000.0
                + (now.getTimeZone().inDaylightTime(now.getTime()) ? now.get(Calendar.DST_OFFSET) / 3600000.0 : 0);

        double sunrise = sunEventUtcHour(dayOfYear, lat, lngHour, true);
        double sunset = sunEventUtcHour(dayOfYear, lat, lngHour, false);
        if (sunrise < 0 || sunset < 0) return new double[]{-1, -1};
        return new double[]{norm24(sunrise + utcOffsetHours), norm24(sunset + utcOffsetHours)};
    }

    private static double sunEventUtcHour(int dayOfYear, double lat, double lngHour, boolean rising) {
        double t = dayOfYear + ((rising ? 6.0 : 18.0) - lngHour) / 24.0;
        double m = (0.9856 * t) - 3.289;
        double mRad = Math.toRadians(m);
        double l = m + (1.916 * Math.sin(mRad)) + (0.020 * Math.sin(2 * mRad)) + 282.634;
        l = norm360(l);
        double lRad = Math.toRadians(l);

        double ra = Math.toDegrees(Math.atan(0.91764 * Math.tan(lRad)));
        ra = norm360(ra);
        double lQuadrant = Math.floor(l / 90.0) * 90.0;
        double raQuadrant = Math.floor(ra / 90.0) * 90.0;
        ra = ra + (lQuadrant - raQuadrant);
        ra = ra / 15.0;

        double sinDec = 0.39782 * Math.sin(lRad);
        double cosDec = Math.cos(Math.asin(sinDec));

        double zenith = 90.833; // official sunrise/sunset, includes atmospheric refraction
        double latRad = Math.toRadians(lat);
        double cosH = (Math.cos(Math.toRadians(zenith)) - (sinDec * Math.sin(latRad))) / (cosDec * Math.cos(latRad));
        if (cosH > 1.0 || cosH < -1.0) return -1; // sun never rises/sets at this latitude/date

        double h = rising ? 360.0 - Math.toDegrees(Math.acos(cosH)) : Math.toDegrees(Math.acos(cosH));
        h = h / 15.0;

        double localMeanTime = h + ra - (0.06571 * t) - 6.622;
        return norm24(localMeanTime - lngHour);
    }

    private static double norm24(double hours) {
        double r = hours % 24.0;
        return r < 0 ? r + 24.0 : r;
    }

    private static double julianDay(Calendar cal) {
        Calendar utc = (Calendar) cal.clone();
        utc.setTimeZone(TimeZone.getTimeZone("UTC"));
        int year = utc.get(Calendar.YEAR);
        int month = utc.get(Calendar.MONTH) + 1;
        double day = utc.get(Calendar.DAY_OF_MONTH)
                + utc.get(Calendar.HOUR_OF_DAY) / 24.0
                + utc.get(Calendar.MINUTE) / 1440.0
                + utc.get(Calendar.SECOND) / 86400.0;
        if (month <= 2) {
            year -= 1;
            month += 12;
        }
        int a = year / 100;
        int b = 2 - a + a / 4;
        return Math.floor(365.25 * (year + 4716)) + Math.floor(30.6001 * (month + 1)) + day + b - 1524.5;
    }

    /** Low-precision Sun ecliptic longitude (Meeus ch. 25, truncated). */
    private static double sunEclipticLongitude(double t) {
        double l0 = norm360(280.46646 + 36000.76983 * t + 0.0003032 * t * t);
        double m = norm360(357.52911 + 35999.05029 * t - 0.0001537 * t * t);
        double mRad = Math.toRadians(m);
        double c = (1.914602 - 0.004817 * t - 0.000014 * t * t) * Math.sin(mRad)
                + (0.019993 - 0.000101 * t) * Math.sin(2 * mRad)
                + 0.000289 * Math.sin(3 * mRad);
        return norm360(l0 + c);
    }

    /** Low-precision Moon ecliptic longitude (Meeus ch. 47, truncated to main terms). */
    private static double moonEclipticLongitude(double t) {
        double lp = norm360(218.3164477 + 481267.88123421 * t);
        double d = norm360(297.8501921 + 445267.1114034 * t);
        double m = norm360(357.5291092 + 35999.0502909 * t);
        double mp = norm360(134.9633964 + 477198.8675055 * t);

        double dRad = Math.toRadians(d);
        double mRad = Math.toRadians(m);
        double mpRad = Math.toRadians(mp);

        double dLon = 6.288774 * Math.sin(mpRad)
                + 1.274027 * Math.sin(2 * dRad - mpRad)
                + 0.658314 * Math.sin(2 * dRad)
                + 0.213618 * Math.sin(2 * mpRad)
                - 0.185116 * Math.sin(mRad)
                - 0.114332 * Math.sin(2 * (dRad - mpRad) + mpRad - dRad + dRad)
                + 0.058793 * Math.sin(2 * (dRad - mpRad))
                + 0.057066 * Math.sin(2 * dRad - mRad - mpRad)
                + 0.053322 * Math.sin(2 * dRad + mpRad)
                + 0.045758 * Math.sin(2 * dRad - mRad);

        return norm360(lp + dLon);
    }

    private static double norm360(double deg) {
        double r = deg % 360.0;
        return r < 0 ? r + 360.0 : r;
    }
}
