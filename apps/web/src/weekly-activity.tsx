import { query } from "./data";
import "./weekly-activity.css";

interface ActivityDay {
  date: string;
  imagesReceived: number;
  videosReceived: number;
  imagesUploaded: number;
  videosUploaded: number;
}

const shortDate = new Intl.DateTimeFormat("en-GB", {
  timeZone: "UTC",
  day: "numeric",
  month: "short",
});
const labels = ["Images received", "Videos received", "Uploaded"];
const uploaded = (day: ActivityDay) => day.imagesUploaded + day.videosUploaded;
const counts = (day: ActivityDay) => [
  day.imagesReceived,
  day.videosReceived,
  uploaded(day),
];
const dayLabel = (day: ActivityDay) =>
  shortDate.format(new Date(day.date + "T00:00:00Z"));

export function WeeklyActivity() {
  const activity = query<ActivityDay[]>("/dashboard/activity");
  const days = activity.data;
  const max = days && Math.max(...days.flatMap(counts));
  return (
    <section className="panel weeklyActivity" aria-labelledby="activity-title">
      <small>LAST 7 DAYS · BANGKOK TIME</small>
      <h2 id="activity-title">Media activity</h2>
      <p>
        Images and videos are counted when received. Uploads are counted when
        they finish. Today is partial.
      </p>
      {!days ? (
        <p role="status">
          {activity.isError
            ? "Daily activity could not be loaded."
            : "Loading daily activity…"}
        </p>
      ) : (
        <>
          <ul className="activityLegend" aria-label="Chart legend">
            {labels.map((label, i) => (
              <li key={label}>
                <i className={"series" + i} aria-hidden="true" />
                {label}
              </li>
            ))}
          </ul>
          {!max && <p>No media received or uploaded in this period.</p>}
          <div className="activityPlot" aria-hidden="true">
            <div className="activityScale">
              <span>{max || 1}</span>
              <span>0</span>
            </div>
            {days.map((day) => (
              <div className="activityDay" key={day.date}>
                <div className="activityBars">
                  {counts(day).map((count, i) => (
                    <div
                      key={i}
                      className={"series" + i}
                      style={{ height: (100 * count) / (max || 1) + "%" }}
                      title={dayLabel(day) + " · " + labels[i] + ": " + count}
                    />
                  ))}
                </div>
                <time dateTime={day.date}>{dayLabel(day)}</time>
              </div>
            ))}
          </div>
          <details>
            <summary>Daily counts and upload breakdown</summary>
            <div className="activityTable">
              <table>
                <caption>Media items per Bangkok day</caption>
                <thead>
                  <tr>
                    <th rowSpan={2} scope="col">
                      Date
                    </th>
                    <th colSpan={2}>Received</th>
                    <th colSpan={3}>Uploaded</th>
                  </tr>
                  <tr>
                    {["Images", "Videos", "Images", "Videos", "Total"].map(
                      (text, i) => (
                        <th key={i} scope="col">
                          {text}
                        </th>
                      ),
                    )}
                  </tr>
                </thead>
                <tbody>
                  {days.map((day) => (
                    <tr key={day.date}>
                      <th scope="row">{dayLabel(day)}</th>
                      <td>{day.imagesReceived}</td>
                      <td>{day.videosReceived}</td>
                      <td>{day.imagesUploaded}</td>
                      <td>{day.videosUploaded}</td>
                      <td>{uploaded(day)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </details>
        </>
      )}
    </section>
  );
}
